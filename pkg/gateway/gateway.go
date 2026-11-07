package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/circuitbreaker"
	"github.com/imMamdouhaboammar/liteRouter/pkg/decision"
	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

// ChatCompletionRequest models the OpenAI-compatible request payload.
type ChatCompletionRequest struct {
	Model       string                   `json:"model"`
	Messages    []map[string]interface{} `json:"messages"`
	Stream      bool                     `json:"stream"`
	Temperature float64                  `json:"temperature,omitempty"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
}

// Server provides the high-concurrency L7 API gateway.
type Server struct {
	kvStore     *kvstore.KvStore
	decisionEng *decision.Engine
	failover    *circuitbreaker.FailoverCoordinator
	httpClient  *http.Client
}

func NewServer(
	kv *kvstore.KvStore,
	eng *decision.Engine,
	failover *circuitbreaker.FailoverCoordinator,
) *Server {
	if failover == nil {
		failover = circuitbreaker.NewFailoverCoordinator(circuitbreaker.NewManager())
	}
	return &Server{
		kvStore:     kv,
		decisionEng: eng,
		failover:    failover,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 200,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Handler returns the HTTP mux for the gateway.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleListModels)
	mux.HandleFunc("/healthz", s.handleHealthz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","engine":"liteRouter"}`))
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models := s.kvStore.Registry().ListModels()
	data, _ := json.Marshal(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// 1. Extract prompt text from messages to profile intent
	var promptText stringsBuilder
	for _, m := range req.Messages {
		if content, ok := m["content"].(string); ok {
			promptText.WriteString(content)
			promptText.WriteString(" ")
		}
	}

	tenantTier := r.Header.Get("X-Tenant-Tier")
	if tenantTier == "" {
		tenantTier = "standard"
	}

	profile := decision.ProfileRequest(promptText.String(), tenantTier)

	// 2. Obtain snapshot of live candidates from KvStore
	candidates := s.kvStore.Registry().SnapshotCandidates()
	if len(candidates) == 0 {
		http.Error(w, "No active upstream models available in KvStore", http.StatusServiceUnavailable)
		return
	}

	// 3. Compute optimal Pareto route using Decision Engine
	routePlan, err := s.decisionEng.Route(profile, candidates)
	if err != nil {
		http.Error(w, fmt.Sprintf("Routing error: %v", err), http.StatusBadGateway)
		return
	}

	// 4. Execute with transparent circuit-breaker failover
	executor := func(ctx context.Context, route decision.SelectedRoute) (*http.Response, error) {
		// If base URL starts with mock://, use internal test responder
		if len(route.BaseURL) >= 7 && route.BaseURL[:7] == "mock://" {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			rec.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"content":"Mock response from %s/%s"}}]}`, route.ProviderID, route.ModelID)))
			return rec.Result(), nil
		}

		targetURL := fmt.Sprintf("%s/chat/completions", route.BaseURL)
		upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		upstreamReq.Header = r.Header.Clone()
		upstreamReq.Header.Set("Content-Type", "application/json")

		return s.httpClient.Do(upstreamReq)
	}

	result, err := s.failover.Execute(r.Context(), routePlan, executor)
	if err != nil {
		http.Error(w, fmt.Sprintf("All upstream routes failed: %v", err), http.StatusBadGateway)
		return
	}
	defer result.Response.Body.Close()

	// 5. Inject LiteRouter Telemetry Headers
	w.Header().Set("X-LiteRouter-Provider", result.SuccessfulRoute.ProviderID)
	w.Header().Set("X-LiteRouter-Model", result.SuccessfulRoute.ModelID)
	w.Header().Set("X-LiteRouter-Attempts", strconv.Itoa(result.Attempts))
	w.Header().Set("X-LiteRouter-Latency-Us", strconv.FormatInt(routePlan.ExecutionMicros, 10))

	// Copy upstream response headers (content-type, etc.)
	for k, vv := range result.Response.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(result.Response.StatusCode)

	// 6. Stream or write back body
	if req.Stream {
		flusher, ok := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, readErr := result.Response.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				if ok {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}
	} else {
		io.Copy(w, result.Response.Body)
	}
}

type stringsBuilder struct {
	buf bytes.Buffer
}

func (s *stringsBuilder) WriteString(str string) {
	s.buf.WriteString(str)
}

func (s *stringsBuilder) String() string {
	return s.buf.String()
}
