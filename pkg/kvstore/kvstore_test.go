package kvstore

import (
	"context"
	"testing"
	"time"
)

func TestKvStorePutGet(t *testing.T) {
	kv := NewKvStore("node-1", 100*time.Millisecond)
	defer kv.Close()

	entry, err := kv.Put("key1", []byte("hello openr"), 0)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if entry.Version != 1 {
		t.Fatalf("expected version 1, got %d", entry.Version)
	}

	got, err := kv.Get("key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(got.Data) != "hello openr" {
		t.Fatalf("expected data 'hello openr', got %s", string(got.Data))
	}
}

func TestKvStoreConflictResolution(t *testing.T) {
	kv1 := NewKvStore("node-alpha", 100*time.Millisecond)
	defer kv1.Close()

	_, err := kv1.Put("config:routing", []byte("v1-alpha"), 0)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Remote update with higher version should win
	remoteV2 := ValueTuple{
		Key:          "config:routing",
		Version:      2,
		OriginatorID: "node-beta",
		Hash:         ComputeHash([]byte("v2-beta")),
		Data:         []byte("v2-beta"),
	}

	merged, err := kv1.MergeUpdate(remoteV2)
	if err != nil || !merged {
		t.Fatalf("expected higher version to win, merged=%v, err=%v", merged, err)
	}

	current, _ := kv1.Get("config:routing")
	if string(current.Data) != "v2-beta" {
		t.Fatalf("expected 'v2-beta', got %s", string(current.Data))
	}

	// Stale update (version 1) should be rejected
	staleV1 := ValueTuple{
		Key:          "config:routing",
		Version:      1,
		OriginatorID: "node-gamma",
		Hash:         ComputeHash([]byte("stale")),
		Data:         []byte("stale"),
	}

	merged, err = kv1.MergeUpdate(staleV1)
	if merged || err != ErrUpdateRejected {
		t.Fatalf("expected stale update to be rejected, merged=%v, err=%v", merged, err)
	}

	// Tie-break test: Same version (v2), but originator 'node-omega' > 'node-beta'
	tieBreaker := ValueTuple{
		Key:          "config:routing",
		Version:      2,
		OriginatorID: "node-omega",
		Hash:         ComputeHash([]byte("v2-omega")),
		Data:         []byte("v2-omega"),
	}

	merged, err = kv1.MergeUpdate(tieBreaker)
	if err != nil || !merged {
		t.Fatalf("expected tie-breaker higher originator to win, merged=%v, err=%v", merged, err)
	}

	current, _ = kv1.Get("config:routing")
	if current.OriginatorID != "node-omega" {
		t.Fatalf("expected originator 'node-omega', got %s", current.OriginatorID)
	}
}

func TestKvStoreTTLExpiration(t *testing.T) {
	kv := NewKvStore("node-1", 50*time.Millisecond)
	defer kv.Close()

	// Short TTL: 100ms
	_, err := kv.Put("ephemeral:heartbeat", []byte("alive"), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Immediate get should succeed
	_, err = kv.Get("ephemeral:heartbeat")
	if err != nil {
		t.Fatalf("expected key to exist, got: %v", err)
	}

	// Wait 250ms for sweeper to evict
	time.Sleep(250 * time.Millisecond)

	_, err = kv.Get("ephemeral:heartbeat")
	if err != ErrKeyNotFound && err != ErrKeyExpired {
		t.Fatalf("expected key to be expired or deleted, got: %v", err)
	}
}

func TestKvStoreSubscribeDelta(t *testing.T) {
	kv := NewKvStore("node-1", 100*time.Millisecond)
	defer kv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := kv.Subscribe(ctx, "model:")

	// Put matching key
	_, err := kv.Put("model:openai:gpt-4o", []byte(`{"id":"gpt-4o"}`), 0)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Entry.Key != "model:openai:gpt-4o" {
			t.Fatalf("unexpected key in delta event: %s", ev.Entry.Key)
		}
		if ev.Type != EventPut {
			t.Fatalf("expected EventPut, got %v", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for delta event")
	}
}

func TestModelRegistryAndCompositeMetric(t *testing.T) {
	reg := NewModelRegistry()

	prov := Provider{
		ID:             "openai-east",
		Name:           "OpenAI East US",
		BaseURL:        "https://api.openai.com/v1",
		Status:         StatusHealthy,
		MaxConcurrency: 100,
	}
	reg.RegisterProvider(prov)

	mod := Model{
		ID:         "gpt-4o",
		ProviderID: "openai-east",
		Capabilities: CapabilityScores{
			Reasoning:     4.8,
			Code:          4.9,
			Math:          4.7,
			ContextWindow: 128000,
		},
		Pricing: Pricing{
			PromptPerMillionTokens:     2.50,
			CompletionPerMillionTokens: 10.00,
		},
		HealthScore: 1.0,
		Active:      true,
	}
	reg.RegisterModel(mod)

	metric := LinkMetric{
		ProviderID:   "openai-east",
		ModelID:      "gpt-4o",
		RTTEMA:       45.0,
		TTFTEMA:      210.0,
		ErrorRate5xx: 0.0,
		ErrorRate429: 0.0,
	}
	cost := metric.ComputeCompositeCost(mod.Pricing)
	if cost <= 0 {
		t.Fatalf("expected positive composite cost, got %f", cost)
	}
	reg.UpdateMetric(metric)

	candidates := reg.SnapshotCandidates()
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].ModelID != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %s", candidates[0].ModelID)
	}
}
