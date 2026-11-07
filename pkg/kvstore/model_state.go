package kvstore

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ProviderStatus represents the health and connectivity state of an AI provider.
type ProviderStatus string

const (
	StatusHealthy  ProviderStatus = "healthy"
	StatusDegraded ProviderStatus = "degraded"
	StatusDown     ProviderStatus = "down"
)

// CapabilityScores stores quantitative evaluation metrics (1.0 to 5.0) for an AI model.
type CapabilityScores struct {
	Reasoning          float64 `json:"reasoning"`           // Complex chain-of-thought and math
	Code               float64 `json:"code"`                // Syntax, logic, and debugging
	Math               float64 `json:"math"`                // Formal logic and arithmetic
	Creative           float64 `json:"creative"`            // Open-ended and literary output
	ContextWindow      int     `json:"context_window"`      // Maximum supported context tokens
	LatencySensitivity float64 `json:"latency_sensitivity"` // 1.0 = batch/relaxed, 5.0 = real-time interactive
}

// Pricing defines the monetary cost per million tokens in USD.
type Pricing struct {
	PromptPerMillionTokens     float64 `json:"prompt_per_million"`
	CompletionPerMillionTokens float64 `json:"completion_per_million"`
}

// Provider represents a physical or logical upstream AI API endpoint.
type Provider struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	BaseURL         string         `json:"base_url"`
	APIKeyEnv       string         `json:"api_key_env"`
	Status          ProviderStatus `json:"status"`
	Weight          float64        `json:"weight"`
	MaxConcurrency  int            `json:"max_concurrency"`
	CurrentInflight int64          `json:"current_inflight"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// Model represents a specific model served by a provider.
type Model struct {
	ID           string           `json:"id"`          // e.g. "gpt-4o", "claude-3-5-sonnet", "deepseek-v3"
	ProviderID   string           `json:"provider_id"` // e.g. "openai-east", "anthropic-main", "deepseek-api"
	Capabilities CapabilityScores `json:"capabilities"`
	Pricing      Pricing          `json:"pricing"`
	HealthScore  float64          `json:"health_score"` // 0.0 (offline) to 1.0 (flawless)
	Active       bool             `json:"active"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// LinkMetric captures real-time networking and operational metrics for an adjacency.
type LinkMetric struct {
	ProviderID       string    `json:"provider_id"`
	ModelID          string    `json:"model_id"`
	RTTEMA           float64   `json:"rtt_ema_ms"`     // Rolling Round-Trip-Time (ms)
	TTFTEMA          float64   `json:"ttft_ema_ms"`    // Rolling Time-To-First-Token (ms)
	TPOTEMA          float64   `json:"tpot_ema_ms"`    // Rolling Time-Per-Output-Token (ms)
	ErrorRate5xx     float64   `json:"error_rate_5xx"` // 0.0 to 1.0 windowed error rate
	ErrorRate429     float64   `json:"error_rate_429"` // Rate-limit backoff factor
	InflightRequests int64     `json:"inflight"`
	CompositeCost    float64   `json:"composite_cost"` // SPF routing cost weight (lower = better)
	UpdatedAt        time.Time `json:"updated_at"`
}

// ComputeCompositeCost calculates the Dijkstra routing link weight based on cost, latency, errors, and congestion.
func (m *LinkMetric) ComputeCompositeCost(price Pricing) float64 {
	// Normalized Weights:
	// alpha: Token Cost ($ / 1M prompt tokens)
	// beta:  Time to first token (seconds)
	// gamma: 429 Rate-limit penalty
	// delta: 5xx Error penalty
	// mu:    Queue congestion
	const (
		alpha = 0.25 // Cost sensitivity
		beta  = 0.35 // Latency sensitivity
		gamma = 0.20 // 429 penalty
		delta = 0.15 // 5xx outage penalty
		mu    = 0.05 // Congestion penalty
	)

	costWeight := price.PromptPerMillionTokens * 10.0 // Normalize roughly [0, 50]
	latencySec := (m.TTFTEMA + m.RTTEMA) / 1000.0     // Convert ms to seconds
	latencyWeight := latencySec * 20.0

	penalty429 := m.ErrorRate429 * 100.0
	penalty5xx := m.ErrorRate5xx * 200.0
	congestion := float64(m.InflightRequests) * 2.0

	cost := (alpha * costWeight) +
		(beta * latencyWeight) +
		(gamma * penalty429) +
		(delta * penalty5xx) +
		(mu * congestion)

	if cost < 0.1 {
		cost = 0.1
	}
	m.CompositeCost = cost
	return cost
}

// ModelRegistry holds in-memory indexed models and providers for zero-allocation lookups.
type ModelRegistry struct {
	mu        sync.RWMutex
	providers map[string]*Provider
	models    map[string]*Model // key: providerID + "/" + modelID
	metrics   map[string]*LinkMetric
}

func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		providers: make(map[string]*Provider),
		models:    make(map[string]*Model),
		metrics:   make(map[string]*LinkMetric),
	}
}

func ModelKey(providerID, modelID string) string {
	return fmt.Sprintf("%s/%s", providerID, modelID)
}

func (r *ModelRegistry) RegisterProvider(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.UpdatedAt = time.Now()
	if p.Status == "" {
		p.Status = StatusHealthy
	}
	r.providers[p.ID] = &p
}

func (r *ModelRegistry) GetProvider(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	if !ok {
		return Provider{}, false
	}
	return *p, true
}

func (r *ModelRegistry) RegisterModel(m Model) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m.UpdatedAt = time.Now()
	r.models[ModelKey(m.ProviderID, m.ID)] = &m
}

func (r *ModelRegistry) GetModel(providerID, modelID string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[ModelKey(providerID, modelID)]
	if !ok {
		return Model{}, false
	}
	return *m, true
}

func (r *ModelRegistry) ListModels() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		res = append(res, *m)
	}
	return res
}

func (r *ModelRegistry) UpdateMetric(m LinkMetric) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m.UpdatedAt = time.Now()
	key := ModelKey(m.ProviderID, m.ModelID)
	r.metrics[key] = &m
}

func (r *ModelRegistry) GetMetric(providerID, modelID string) (LinkMetric, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.metrics[ModelKey(providerID, modelID)]
	if !ok {
		return LinkMetric{}, false
	}
	return *m, true
}

// CandidateModel encapsulates all routing attributes for the decision engine.
type CandidateModel struct {
	ProviderID    string           `json:"provider_id"`
	ModelID       string           `json:"model_id"`
	BaseURL       string           `json:"base_url"`
	Capabilities  CapabilityScores `json:"capabilities"`
	Pricing       Pricing          `json:"pricing"`
	HealthScore   float64          `json:"health_score"`
	CompositeCost float64          `json:"composite_cost"`
	TTFTEMA       float64          `json:"ttft_ema_ms"`
}

func (r *ModelRegistry) SnapshotCandidates() []CandidateModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var candidates []CandidateModel
	for _, m := range r.models {
		if !m.Active {
			continue
		}
		p, hasProv := r.providers[m.ProviderID]
		if !hasProv || p.Status == StatusDown {
			continue
		}

		key := ModelKey(m.ProviderID, m.ID)
		metric, hasMetric := r.metrics[key]
		cost := 1.0
		ttft := 250.0
		if hasMetric {
			cost = metric.CompositeCost
			ttft = metric.TTFTEMA
		}

		candidates = append(candidates, CandidateModel{
			ProviderID:    m.ProviderID,
			ModelID:       m.ID,
			BaseURL:       p.BaseURL,
			Capabilities:  m.Capabilities,
			Pricing:       m.Pricing,
			HealthScore:   m.HealthScore,
			CompositeCost: cost,
			TTFTEMA:       ttft,
		})
	}
	return candidates
}

func (m *Model) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func UnmarshalModel(data []byte) (*Model, error) {
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
