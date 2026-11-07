package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/circuitbreaker"
	"github.com/imMamdouhaboammar/liteRouter/pkg/decision"
	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

func setupTestServer() *Server {
	kv := kvstore.NewKvStore("test-gw", 100*time.Millisecond)

	prov := kvstore.Provider{
		ID:      "mock-openai",
		Name:    "Mock OpenAI Provider",
		BaseURL: "mock://openai",
		Status:  kvstore.StatusHealthy,
	}
	kv.Registry().RegisterProvider(prov)

	mod := kvstore.Model{
		ID:         "gpt-4o",
		ProviderID: "mock-openai",
		Capabilities: kvstore.CapabilityScores{
			Reasoning:     4.8,
			Code:          4.8,
			Math:          4.7,
			ContextWindow: 128000,
		},
		Pricing: kvstore.Pricing{
			PromptPerMillionTokens: 2.50,
		},
		HealthScore: 1.0,
		Active:      true,
	}
	kv.Registry().RegisterModel(mod)

	eng := decision.NewEngine()
	cbMgr := circuitbreaker.NewManager()
	failover := circuitbreaker.NewFailoverCoordinator(cbMgr)

	return NewServer(kv, eng, failover)
}

func TestGatewayHealthz(t *testing.T) {
	srv := setupTestServer()
	defer srv.kvStore.Close()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	if !bytes.Contains(rec.Body.Bytes(), []byte("liteRouter")) {
		t.Fatalf("expected liteRouter in body, got: %s", rec.Body.String())
	}
}

func TestGatewayListModels(t *testing.T) {
	srv := setupTestServer()
	defer srv.kvStore.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse models response: %v", err)
	}

	if resp["object"] != "list" {
		t.Fatalf("expected object: list, got %v", resp["object"])
	}
}

func TestGatewayChatCompletionsRouting(t *testing.T) {
	srv := setupTestServer()
	defer srv.kvStore.Close()

	payload := ChatCompletionRequest{
		Model: "auto",
		Messages: []map[string]interface{}{
			{"role": "user", "content": "Write a python func `def solve():` for me"},
		},
		Temperature: 0.7,
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Tier", "standard")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	// Verify Telemetry Headers
	provHdr := rec.Header().Get("X-LiteRouter-Provider")
	if provHdr != "mock-openai" {
		t.Fatalf("expected X-LiteRouter-Provider header 'mock-openai', got '%s'", provHdr)
	}

	modelHdr := rec.Header().Get("X-LiteRouter-Model")
	if modelHdr != "gpt-4o" {
		t.Fatalf("expected X-LiteRouter-Model header 'gpt-4o', got '%s'", modelHdr)
	}

	attemptsHdr := rec.Header().Get("X-LiteRouter-Attempts")
	if attemptsHdr != "1" {
		t.Fatalf("expected X-LiteRouter-Attempts header '1', got '%s'", attemptsHdr)
	}
}
