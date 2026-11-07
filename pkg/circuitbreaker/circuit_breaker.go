package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/decision"
)

var (
	ErrCircuitOpen         = errors.New("circuit breaker: circuit is open (upstream unhealthy)")
	ErrAllFallbacksExhaust = errors.New("circuit breaker: all primary and fallback upstreams failed")
)

type State string

const (
	StateClosed   State = "closed"    // Normal healthy operation
	StateHalfOpen State = "half-open" // Probing recovery
	StateOpen     State = "open"      // Tripped, blocking calls
)

type CircuitBreaker struct {
	mu                sync.RWMutex
	name              string
	state             State
	failureThreshold  int // Consecutive failures to trip (default: 3)
	consecutiveFails  int
	successThreshold  int // Consecutive successes in HalfOpen to close (default: 2)
	consecutiveSucc   int
	cooldownDuration  time.Duration // Time to wait in Open state before testing HalfOpen
	lastStateChange   time.Time
	lastFailure       time.Time
	rateLimitCooldown time.Duration // Adaptive 429 penalty
}

func NewCircuitBreaker(name string, failureThreshold int, cooldown time.Duration) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if cooldown <= 0 {
		cooldown = 10 * time.Second
	}

	return &CircuitBreaker{
		name:              name,
		state:             StateClosed,
		failureThreshold:  failureThreshold,
		successThreshold:  2,
		cooldownDuration:  cooldown,
		rateLimitCooldown: 5 * time.Second,
		lastStateChange:   time.Now(),
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check if Open state has passed cooldown
	if cb.state == StateOpen {
		if time.Since(cb.lastStateChange) > cb.cooldownDuration {
			cb.state = StateHalfOpen
			cb.consecutiveSucc = 0
			cb.lastStateChange = time.Now()
		}
	}

	return cb.state
}

func (cb *CircuitBreaker) AllowRequest() bool {
	state := cb.State()
	return state == StateClosed || state == StateHalfOpen
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0

	if cb.state == StateHalfOpen {
		cb.consecutiveSucc++
		if cb.consecutiveSucc >= cb.successThreshold {
			cb.state = StateClosed
			cb.lastStateChange = time.Now()
		}
	}
}

func (cb *CircuitBreaker) RecordFailure(statusCode int) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()
	cb.consecutiveFails++

	// 429 Too Many Requests -> Immediate trip with adaptive backoff
	if statusCode == http.StatusTooManyRequests {
		cb.state = StateOpen
		cb.lastStateChange = time.Now()
		cb.cooldownDuration = cb.rateLimitCooldown
		cb.rateLimitCooldown *= 2 // Exponential backoff for repeated 429s
		if cb.rateLimitCooldown > 60*time.Second {
			cb.rateLimitCooldown = 60 * time.Second
		}
		return
	}

	// 5xx Server Outages or connection reset
	if cb.consecutiveFails >= cb.failureThreshold {
		cb.state = StateOpen
		cb.lastStateChange = time.Now()
	}
}

// Manager coordinates circuit breakers across all providers and models.
type Manager struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

func NewManager() *Manager {
	return &Manager{
		breakers: make(map[string]*CircuitBreaker),
	}
}

func (m *Manager) GetBreaker(providerID, modelID string) *CircuitBreaker {
	key := fmt.Sprintf("%s/%s", providerID, modelID)
	m.mu.Lock()
	defer m.mu.Unlock()

	cb, exists := m.breakers[key]
	if !exists {
		cb = NewCircuitBreaker(key, 3, 5*time.Second)
		m.breakers[key] = cb
	}
	return cb
}

// UpstreamExecutor performs the actual HTTP invocation against an upstream model.
type UpstreamExecutor func(ctx context.Context, route decision.SelectedRoute) (*http.Response, error)

// FailoverCoordinator executes requests through primary route with transparent multi-tier fallback.
type FailoverCoordinator struct {
	manager *Manager
}

func NewFailoverCoordinator(mgr *Manager) *FailoverCoordinator {
	if mgr == nil {
		mgr = NewManager()
	}
	return &FailoverCoordinator{manager: mgr}
}

// ExecutionResult wraps the response and the route that successfully fulfilled the request.
type ExecutionResult struct {
	Response        *http.Response
	SuccessfulRoute decision.SelectedRoute
	Attempts        int
	Failovers       []string // IDs of routes that failed before success
}

func (fc *FailoverCoordinator) Execute(
	ctx context.Context,
	plan *decision.RouteDecision,
	executor UpstreamExecutor,
) (*ExecutionResult, error) {
	if plan == nil {
		return nil, errors.New("failover: route decision cannot be nil")
	}

	// Build candidate chain: Primary followed by Fallbacks
	routes := make([]decision.SelectedRoute, 0, 1+len(plan.Fallbacks))
	routes = append(routes, plan.Primary)
	routes = append(routes, plan.Fallbacks...)

	var failovers []string

	for attempt, route := range routes {
		cb := fc.manager.GetBreaker(route.ProviderID, route.ModelID)
		if !cb.AllowRequest() {
			failovers = append(failovers, fmt.Sprintf("%s/%s (circuit-open)", route.ProviderID, route.ModelID))
			continue
		}

		resp, err := executor(ctx, route)
		if err != nil {
			cb.RecordFailure(http.StatusServiceUnavailable)
			failovers = append(failovers, fmt.Sprintf("%s/%s (err: %v)", route.ProviderID, route.ModelID, err))
			continue
		}

		// Check for upstream error status codes that warrant failover
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			cb.RecordFailure(resp.StatusCode)
			resp.Body.Close()
			failovers = append(failovers, fmt.Sprintf("%s/%s (http %d)", route.ProviderID, route.ModelID, resp.StatusCode))
			continue
		}

		// Success!
		cb.RecordSuccess()
		return &ExecutionResult{
			Response:        resp,
			SuccessfulRoute: route,
			Attempts:        attempt + 1,
			Failovers:       failovers,
		}, nil
	}

	return nil, fmt.Errorf("%w: failed routes: %v", ErrAllFallbacksExhaust, failovers)
}
