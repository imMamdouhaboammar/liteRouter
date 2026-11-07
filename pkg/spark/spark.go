package spark

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

// ProviderTarget describes the upstream to probe.
type ProviderTarget struct {
	ProviderID string
	ModelID    string
	BaseURL    string
	Endpoint   string
	Headers    map[string]string
}

// ProbeResult contains telemetry from an adjacency health probe.
type ProbeResult struct {
	StatusCode int
	Latency    time.Duration
	TTFT       time.Duration
	Err        error
}

// Prober defines the pluggable network probing mechanism.
type Prober interface {
	Probe(ctx context.Context, target ProviderTarget) ProbeResult
}

// HTTPProber executes real network probes using lightweight HTTP HEAD / GET / POST.
type HTTPProber struct {
	client *http.Client
}

func NewHTTPProber(timeout time.Duration) *HTTPProber {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &HTTPProber{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (h *HTTPProber) Probe(ctx context.Context, target ProviderTarget) ProbeResult {
	start := time.Now()
	url := fmt.Sprintf("%s%s", target.BaseURL, target.Endpoint)
	if target.Endpoint == "" {
		url = target.BaseURL + "/models"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ProbeResult{Err: err, Latency: time.Since(start)}
	}

	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return ProbeResult{Err: err, Latency: latency}
	}
	defer resp.Body.Close()

	return ProbeResult{
		StatusCode: resp.StatusCode,
		Latency:    latency,
		TTFT:       latency, // Approximate TTFT for non-streaming probe
	}
}

// AdjacencyState tracks rolling health and metrics for one provider-model pair.
type AdjacencyState struct {
	Target          ProviderTarget
	Pricing         kvstore.Pricing
	RTTEMA          float64 // ms
	TTFTEMA         float64 // ms
	ErrorRate5xx    float64
	ErrorRate429    float64
	FlapCount       int
	ConsecutiveFail int
	Status          kvstore.ProviderStatus
	LastProbe       time.Time
	LastTransition  time.Time
}

// SparkEngine coordinates background probes and updates KvStore link metrics.
type SparkEngine struct {
	kvStore       *kvstore.KvStore
	prober        Prober
	probeInterval time.Duration
	alpha         float64 // EMA smoothing factor (default 0.2)
	mu            sync.RWMutex
	adjacencies   map[string]*AdjacencyState // key: providerID + "/" + modelID
	stopCh        chan struct{}
	running       int32
}

func NewSparkEngine(kv *kvstore.KvStore, prober Prober, probeInterval time.Duration) *SparkEngine {
	if probeInterval <= 0 {
		probeInterval = 2 * time.Second
	}
	if prober == nil {
		prober = NewHTTPProber(3 * time.Second)
	}

	return &SparkEngine{
		kvStore:       kv,
		prober:        prober,
		probeInterval: probeInterval,
		alpha:         0.2, // OpenR standard smoothing factor
		adjacencies:   make(map[string]*AdjacencyState),
		stopCh:        make(chan struct{}),
	}
}

func (s *SparkEngine) RegisterTarget(target ProviderTarget, price kvstore.Pricing) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := kvstore.ModelKey(target.ProviderID, target.ModelID)
	s.adjacencies[key] = &AdjacencyState{
		Target:         target,
		Pricing:        price,
		RTTEMA:         150.0, // Default optimistic baseline
		TTFTEMA:        300.0,
		Status:         kvstore.StatusHealthy,
		LastProbe:      time.Now(),
		LastTransition: time.Now(),
	}
}

func (s *SparkEngine) Start(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return
	}

	go s.runLoop(ctx)
}

func (s *SparkEngine) Stop() {
	if atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		close(s.stopCh)
	}
}

func (s *SparkEngine) runLoop(ctx context.Context) {
	ticker := time.NewTicker(s.probeInterval)
	defer ticker.Stop()

	// Initial immediate probe wave
	s.probeWave(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.probeWave(ctx)
		}
	}
}

func (s *SparkEngine) probeWave(ctx context.Context) {
	s.mu.RLock()
	targets := make([]*AdjacencyState, 0, len(s.adjacencies))
	for _, adj := range s.adjacencies {
		targets = append(targets, adj)
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	for _, adj := range targets {
		wg.Add(1)
		go func(a *AdjacencyState) {
			defer wg.Done()
			s.probeOne(ctx, a)
		}(adj)
	}
	wg.Wait()
}

func (s *SparkEngine) probeOne(ctx context.Context, adj *AdjacencyState) {
	res := s.prober.Probe(ctx, adj.Target)

	s.mu.Lock()
	defer s.mu.Unlock()

	adj.LastProbe = time.Now()
	latencyMs := float64(res.Latency.Milliseconds())
	ttftMs := float64(res.TTFT.Milliseconds())

	// Apply OpenR Exponential Moving Average
	if adj.RTTEMA == 0 {
		adj.RTTEMA = latencyMs
	} else if latencyMs > 0 {
		adj.RTTEMA = (s.alpha * latencyMs) + ((1 - s.alpha) * adj.RTTEMA)
	}

	if adj.TTFTEMA == 0 {
		adj.TTFTEMA = ttftMs
	} else if ttftMs > 0 {
		adj.TTFTEMA = (s.alpha * ttftMs) + ((1 - s.alpha) * adj.TTFTEMA)
	}

	// Update error metrics based on status codes
	isDown := false
	isDegraded := false

	if res.Err != nil {
		adj.ConsecutiveFail++
		adj.ErrorRate5xx = math.Min(1.0, adj.ErrorRate5xx+0.3)
		if adj.ConsecutiveFail >= 3 {
			isDown = true
		} else {
			isDegraded = true
		}
	} else {
		switch {
		case res.StatusCode == http.StatusTooManyRequests: // 429 Rate Limit
			adj.ErrorRate429 = math.Min(1.0, adj.ErrorRate429+0.4)
			isDegraded = true
			adj.ConsecutiveFail = 0
		case res.StatusCode >= 500: // 5xx Server Error
			adj.ErrorRate5xx = math.Min(1.0, adj.ErrorRate5xx+0.3)
			adj.ConsecutiveFail++
			if adj.ConsecutiveFail >= 3 {
				isDown = true
			} else {
				isDegraded = true
			}
		case res.StatusCode >= 200 && res.StatusCode < 300: // 2xx Success
			adj.ConsecutiveFail = 0
			// Smoothly decay error rates on success
			adj.ErrorRate5xx = math.Max(0.0, adj.ErrorRate5xx*0.7)
			adj.ErrorRate429 = math.Max(0.0, adj.ErrorRate429*0.6)
		}
	}

	// State Machine Evaluation
	var newStatus kvstore.ProviderStatus
	if isDown {
		newStatus = kvstore.StatusDown
	} else if isDegraded || adj.ErrorRate429 > 0.3 || adj.ErrorRate5xx > 0.2 {
		newStatus = kvstore.StatusDegraded
	} else {
		newStatus = kvstore.StatusHealthy
	}

	// Flap Detection
	if newStatus != adj.Status {
		if time.Since(adj.LastTransition) < 10*time.Second {
			adj.FlapCount++
		} else {
			adj.FlapCount = 0
		}
		adj.Status = newStatus
		adj.LastTransition = time.Now()
	}

	// Compute link metric with dampening penalty
	flapPenalty := math.Pow(1.5, float64(adj.FlapCount))
	metric := kvstore.LinkMetric{
		ProviderID:   adj.Target.ProviderID,
		ModelID:      adj.Target.ModelID,
		RTTEMA:       adj.RTTEMA,
		TTFTEMA:      adj.TTFTEMA * flapPenalty,
		ErrorRate5xx: adj.ErrorRate5xx,
		ErrorRate429: adj.ErrorRate429,
		UpdatedAt:    time.Now(),
	}
	metric.ComputeCompositeCost(adj.Pricing)

	// Publish updated metric into KvStore and ModelRegistry
	s.kvStore.Registry().UpdateMetric(metric)

	// Update model health score (1.0 = healthy, 0.5 = degraded, 0.0 = down)
	var healthScore float64 = 1.0
	switch adj.Status {
	case kvstore.StatusDegraded:
		healthScore = 0.5
	case kvstore.StatusDown:
		healthScore = 0.0
	}

	modelKey := kvstore.ModelKey(adj.Target.ProviderID, adj.Target.ModelID)
	if existingModel, ok := s.kvStore.Registry().GetModel(adj.Target.ProviderID, adj.Target.ModelID); ok {
		existingModel.HealthScore = healthScore
		s.kvStore.Registry().RegisterModel(existingModel)
	}

	// Update provider status
	if prov, ok := s.kvStore.Registry().GetProvider(adj.Target.ProviderID); ok {
		prov.Status = adj.Status
		s.kvStore.Registry().RegisterProvider(prov)
	}

	_ = modelKey
}

func (s *SparkEngine) GetAdjacency(providerID, modelID string) (AdjacencyState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := kvstore.ModelKey(providerID, modelID)
	adj, ok := s.adjacencies[key]
	if !ok {
		return AdjacencyState{}, false
	}
	return *adj, true
}
