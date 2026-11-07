package spark

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

type mockProber struct {
	statusCode int
	latency    time.Duration
	err        error
}

func (m *mockProber) Probe(ctx context.Context, target ProviderTarget) ProbeResult {
	return ProbeResult{
		StatusCode: m.statusCode,
		Latency:    m.latency,
		TTFT:       m.latency,
		Err:        m.err,
	}
}

func TestSparkEngineHealthyProbing(t *testing.T) {
	kv := kvstore.NewKvStore("node-1", 100*time.Millisecond)
	defer kv.Close()

	// Register provider and model in registry
	prov := kvstore.Provider{
		ID:      "openai-test",
		Name:    "OpenAI Test",
		BaseURL: "http://mock-openai",
		Status:  kvstore.StatusHealthy,
	}
	kv.Registry().RegisterProvider(prov)

	mod := kvstore.Model{
		ID:         "gpt-4o",
		ProviderID: "openai-test",
		Pricing: kvstore.Pricing{
			PromptPerMillionTokens: 2.50,
		},
		HealthScore: 1.0,
		Active:      true,
	}
	kv.Registry().RegisterModel(mod)

	mock := &mockProber{
		statusCode: http.StatusOK,
		latency:    120 * time.Millisecond,
		err:        nil,
	}

	spark := NewSparkEngine(kv, mock, 50*time.Millisecond)
	target := ProviderTarget{
		ProviderID: "openai-test",
		ModelID:    "gpt-4o",
		BaseURL:    "http://mock-openai",
	}
	spark.RegisterTarget(target, mod.Pricing)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spark.Start(ctx)
	defer spark.Stop()

	// Allow one probe wave to execute
	time.Sleep(120 * time.Millisecond)

	adj, found := spark.GetAdjacency("openai-test", "gpt-4o")
	if !found {
		t.Fatal("expected adjacency to be tracked")
	}

	if adj.Status != kvstore.StatusHealthy {
		t.Fatalf("expected healthy status, got %s", adj.Status)
	}

	// Verify metric updated in registry
	metric, found := kv.Registry().GetMetric("openai-test", "gpt-4o")
	if !found {
		t.Fatal("expected link metric in registry")
	}
	if metric.CompositeCost <= 0 {
		t.Fatalf("expected composite cost > 0, got %f", metric.CompositeCost)
	}
}

func TestSparkEngine429Degradation(t *testing.T) {
	kv := kvstore.NewKvStore("node-1", 100*time.Millisecond)
	defer kv.Close()

	prov := kvstore.Provider{
		ID:      "deepseek-test",
		BaseURL: "http://mock-deepseek",
		Status:  kvstore.StatusHealthy,
	}
	kv.Registry().RegisterProvider(prov)

	mod := kvstore.Model{
		ID:         "deepseek-v3",
		ProviderID: "deepseek-test",
		Pricing:    kvstore.Pricing{PromptPerMillionTokens: 0.14},
		Active:     true,
	}
	kv.Registry().RegisterModel(mod)

	mock := &mockProber{
		statusCode: http.StatusTooManyRequests, // 429!
		latency:    300 * time.Millisecond,
	}

	spark := NewSparkEngine(kv, mock, 40*time.Millisecond)
	target := ProviderTarget{
		ProviderID: "deepseek-test",
		ModelID:    "deepseek-v3",
	}
	spark.RegisterTarget(target, mod.Pricing)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spark.Start(ctx)
	defer spark.Stop()

	time.Sleep(120 * time.Millisecond)

	adj, _ := spark.GetAdjacency("deepseek-test", "deepseek-v3")
	if adj.Status != kvstore.StatusDegraded {
		t.Fatalf("expected degraded status after 429, got %s", adj.Status)
	}
	if adj.ErrorRate429 <= 0 {
		t.Fatalf("expected non-zero 429 error rate, got %f", adj.ErrorRate429)
	}
}

func TestSparkEngine5xxOutageToDown(t *testing.T) {
	kv := kvstore.NewKvStore("node-1", 100*time.Millisecond)
	defer kv.Close()

	prov := kvstore.Provider{
		ID:      "anthropic-test",
		BaseURL: "http://mock-anthropic",
		Status:  kvstore.StatusHealthy,
	}
	kv.Registry().RegisterProvider(prov)

	mod := kvstore.Model{
		ID:         "claude-3-5",
		ProviderID: "anthropic-test",
		Pricing:    kvstore.Pricing{PromptPerMillionTokens: 3.0},
		Active:     true,
	}
	kv.Registry().RegisterModel(mod)

	mock := &mockProber{
		statusCode: http.StatusInternalServerError,
		err:        errors.New("connection reset by peer"),
	}

	spark := NewSparkEngine(kv, mock, 30*time.Millisecond)
	target := ProviderTarget{
		ProviderID: "anthropic-test",
		ModelID:    "claude-3-5",
	}
	spark.RegisterTarget(target, mod.Pricing)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spark.Start(ctx)
	defer spark.Stop()

	// Wait for 3+ consecutive failures
	time.Sleep(150 * time.Millisecond)

	adj, _ := spark.GetAdjacency("anthropic-test", "claude-3-5")
	if adj.Status != kvstore.StatusDown {
		t.Fatalf("expected status Down after multiple consecutive 5xx errors, got %s", adj.Status)
	}
}
