package circuitbreaker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/decision"
)

func TestCircuitBreakerTripAndRecover(t *testing.T) {
	cb := NewCircuitBreaker("test-model", 2, 50*time.Millisecond)

	if cb.State() != StateClosed {
		t.Fatalf("expected initial StateClosed, got %s", cb.State())
	}

	// 1st failure
	cb.RecordFailure(http.StatusInternalServerError)
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 1 failure, got %s", cb.State())
	}

	// 2nd failure -> should trip to Open!
	cb.RecordFailure(http.StatusInternalServerError)
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after 2 failures, got %s", cb.State())
	}

	if cb.AllowRequest() {
		t.Fatal("expected AllowRequest() to return false when Open")
	}

	// Wait for cooldown to transition to HalfOpen
	time.Sleep(60 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen after cooldown, got %s", cb.State())
	}

	// 2 successful requests in HalfOpen should recover to Closed
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after recoveries, got %s", cb.State())
	}
}

func TestCircuitBreaker429ImmediateTrip(t *testing.T) {
	cb := NewCircuitBreaker("test-model-429", 3, 50*time.Millisecond)

	// Single 429 should immediately trip to Open
	cb.RecordFailure(http.StatusTooManyRequests)

	if cb.State() != StateOpen {
		t.Fatalf("expected immediate StateOpen upon 429, got %s", cb.State())
	}
}

func TestFailoverCoordinatorTransparentFallback(t *testing.T) {
	mgr := NewManager()
	coord := NewFailoverCoordinator(mgr)

	plan := &decision.RouteDecision{
		Primary: decision.SelectedRoute{
			ProviderID: "primary-prov",
			ModelID:    "unstable-model",
			BaseURL:    "http://primary",
		},
		Fallbacks: []decision.SelectedRoute{
			{
				ProviderID: "backup-prov",
				ModelID:    "reliable-model",
				BaseURL:    "http://backup",
			},
		},
	}

	// Mock executor: fails primary with 503, succeeds on backup
	executor := func(ctx context.Context, route decision.SelectedRoute) (*http.Response, error) {
		if route.ProviderID == "primary-prov" {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable, // 503 Outage
				Body:       io.NopCloser(bytes.NewBufferString(`{"error":"upstream overloaded"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"content":"fallback success"}}]}`)),
		}, nil
	}

	res, err := coord.Execute(context.Background(), plan, executor)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if res.SuccessfulRoute.ProviderID != "backup-prov" {
		t.Fatalf("expected backup provider to succeed, got %s", res.SuccessfulRoute.ProviderID)
	}

	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", res.Attempts)
	}

	if len(res.Failovers) != 1 {
		t.Fatalf("expected 1 recorded failover, got %v", res.Failovers)
	}

	// Verify that the primary circuit breaker recorded the failure
	primaryBreaker := mgr.GetBreaker("primary-prov", "unstable-model")
	if primaryBreaker.consecutiveFails != 1 {
		t.Fatalf("expected 1 consecutive fail on primary breaker, got %d", primaryBreaker.consecutiveFails)
	}
}
