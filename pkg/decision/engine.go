package decision

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

var (
	ErrNoEligibleCandidates = errors.New("decision: no eligible upstream candidates meet constraints")
	ErrAllProvidersDown     = errors.New("decision: all candidate providers are currently offline")
)

type IntentProfile string

const (
	IntentGeneral   IntentProfile = "general"
	IntentCode      IntentProfile = "code"
	IntentReasoning IntentProfile = "reasoning"
	IntentMath      IntentProfile = "math"
	IntentCreative  IntentProfile = "creative"
)

type RequestProfile struct {
	EstimatedTokens   int           `json:"estimated_tokens"`
	Intent            IntentProfile `json:"intent"`
	MinCodeScore      float64       `json:"min_code_score"`
	MinReasoningScore float64       `json:"min_reasoning_score"`
	MaxCostPerM       float64       `json:"max_cost_per_m"`
	TenantTier        string        `json:"tenant_tier"` // "free", "standard", "enterprise"
	MaxLatencyMs      float64       `json:"max_latency_ms"`
}

type SelectedRoute struct {
	ProviderID string  `json:"provider_id"`
	ModelID    string  `json:"model_id"`
	BaseURL    string  `json:"base_url"`
	Rank       int     `json:"rank"`
	Score      float64 `json:"score"`
	Reason     string  `json:"reason"`
}

type RouteDecision struct {
	Primary         SelectedRoute   `json:"primary"`
	Fallbacks       []SelectedRoute `json:"fallbacks"`
	TotalEvaluated  int             `json:"total_evaluated"`
	FilteredCount   int             `json:"filtered_count"`
	ExecutionMicros int64           `json:"execution_micros"`
}

// IntentClassifier inspects prompt text to categorize task intent.
func ClassifyIntent(prompt string) IntentProfile {
	lower := strings.ToLower(prompt)

	// Code indicators
	codeKeywords := []string{"```", "def ", "function ", "func ", "class ", "import ", "sql", "select ", "git ", "npm ", "panic(", "return err", "async def"}
	for _, kw := range codeKeywords {
		if strings.Contains(lower, kw) {
			return IntentCode
		}
	}

	// Math / Logic indicators
	mathKeywords := []string{"calculate", "integral", "derivative", "equation", "theorem", "matrix", "probability", "sqrt", "variance", "stochastic"}
	for _, kw := range mathKeywords {
		if strings.Contains(lower, kw) {
			return IntentMath
		}
	}

	// Reasoning indicators
	reasoningKeywords := []string{"prove", "analyze", "why does", "explain step-by-step", "deduce", "implication", "root cause"}
	for _, kw := range reasoningKeywords {
		if strings.Contains(lower, kw) {
			return IntentReasoning
		}
	}

	// Creative indicators
	creativeKeywords := []string{"story", "poem", "essay", "dialogue", "creative", "screenplay", "novel"}
	for _, kw := range creativeKeywords {
		if strings.Contains(lower, kw) {
			return IntentCreative
		}
	}

	return IntentGeneral
}

// ProfileRequest builds a RequestProfile from prompt parameters.
func ProfileRequest(prompt string, tenantTier string) RequestProfile {
	// BPE token count approximation: ~4 characters per token
	estimatedTokens := len(prompt) / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}

	intent := ClassifyIntent(prompt)

	profile := RequestProfile{
		EstimatedTokens: estimatedTokens,
		Intent:          intent,
		TenantTier:      tenantTier,
	}

	if intent == IntentCode {
		profile.MinCodeScore = 3.5
	} else if intent == IntentReasoning || intent == IntentMath {
		profile.MinReasoningScore = 3.5
	}

	return profile
}

// Engine executes multi-objective Pareto SPF routing.
type Engine struct {
	mu           sync.RWMutex
	policyScript string // Starlark rule script
}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) SetPolicyScript(script string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policyScript = script
}

type scoredCandidate struct {
	candidate kvstore.CandidateModel
	fitness   float64
}

// Route selects the optimal primary and fallback models.
func (e *Engine) Route(req RequestProfile, candidates []kvstore.CandidateModel) (*RouteDecision, error) {
	start := time.Now()
	total := len(candidates)
	if total == 0 {
		return nil, ErrNoEligibleCandidates
	}

	// Dynamic Starlark policy override check (mock/fast parser)
	e.mu.RLock()
	policy := e.policyScript
	e.mu.RUnlock()

	if policy != "" && strings.Contains(policy, "ROUTE_OVERRIDE") {
		// Extract overridden model ID if specified
		for _, line := range strings.Split(policy, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ROUTE_OVERRIDE = \"") {
				override := strings.Trim(strings.TrimPrefix(line, "ROUTE_OVERRIDE = \""), "\"")
				for _, c := range candidates {
					if c.ModelID == override && c.HealthScore > 0.05 {
						return &RouteDecision{
							Primary: SelectedRoute{
								ProviderID: c.ProviderID,
								ModelID:    c.ModelID,
								BaseURL:    c.BaseURL,
								Rank:       0,
								Score:      1.0,
								Reason:     "Selected by dynamic Starlark routing policy",
							},
							Fallbacks:       []SelectedRoute{},
							TotalEvaluated:  total,
							FilteredCount:   1,
							ExecutionMicros: time.Since(start).Microseconds(),
						}, nil
					}
				}
			}
		}
	}

	// Step 1: Filter Hard Constraints
	var eligible []scoredCandidate
	for _, c := range candidates {
		// Must not be down
		if c.HealthScore <= 0.05 {
			continue
		}

		// Context window constraint
		if c.Capabilities.ContextWindow < req.EstimatedTokens {
			continue
		}

		// Capability constraints
		if req.MinCodeScore > 0 && c.Capabilities.Code < req.MinCodeScore {
			continue
		}
		if req.MinReasoningScore > 0 && c.Capabilities.Reasoning < req.MinReasoningScore {
			continue
		}

		// Budget limit
		if req.MaxCostPerM > 0 && c.Pricing.PromptPerMillionTokens > req.MaxCostPerM {
			continue
		}

		// Latency limit
		if req.MaxLatencyMs > 0 && c.TTFTEMA > req.MaxLatencyMs {
			continue
		}

		fitness := calculateFitness(req, c)
		eligible = append(eligible, scoredCandidate{
			candidate: c,
			fitness:   fitness,
		})
	}

	if len(eligible) == 0 {
		return emergencyFallback(candidates, total, start)
	}

	// Step 2: Sort descending by Pareto fitness
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].fitness > eligible[j].fitness
	})

	primary := eligible[0]
	res := &RouteDecision{
		Primary: SelectedRoute{
			ProviderID: primary.candidate.ProviderID,
			ModelID:    primary.candidate.ModelID,
			BaseURL:    primary.candidate.BaseURL,
			Rank:       0,
			Score:      primary.fitness,
			Reason: fmt.Sprintf(
				"Optimal Pareto path: intent=%s, health=%.2f, cost=$%.2f/M, ttft=%.1fms",
				req.Intent,
				primary.candidate.HealthScore,
				primary.candidate.Pricing.PromptPerMillionTokens,
				primary.candidate.TTFTEMA,
			),
		},
		TotalEvaluated:  total,
		FilteredCount:   len(eligible),
		ExecutionMicros: time.Since(start).Microseconds(),
	}

	// Add up to 3 fallback models
	for i := 1; i < len(eligible) && i <= 3; i++ {
		fallback := eligible[i]
		res.Fallbacks = append(res.Fallbacks, SelectedRoute{
			ProviderID: fallback.candidate.ProviderID,
			ModelID:    fallback.candidate.ModelID,
			BaseURL:    fallback.candidate.BaseURL,
			Rank:       i,
			Score:      fallback.fitness,
			Reason: fmt.Sprintf(
				"Fallback rank %d: health=%.2f, cost=$%.2f/M, ttft=%.1fms",
				i,
				fallback.candidate.HealthScore,
				fallback.candidate.Pricing.PromptPerMillionTokens,
				fallback.candidate.TTFTEMA,
			),
		})
	}

	return res, nil
}

func calculateFitness(req RequestProfile, c kvstore.CandidateModel) float64 {
	// Intent-Weighted Quality Component (0.0 to 1.0)
	var quality float64
	switch req.Intent {
	case IntentCode:
		quality = (c.Capabilities.Code*0.7 + c.Capabilities.Reasoning*0.3) / 5.0
	case IntentReasoning, IntentMath:
		quality = (c.Capabilities.Reasoning*0.6 + c.Capabilities.Math*0.4) / 5.0
	case IntentCreative:
		quality = (c.Capabilities.Creative*0.8 + c.Capabilities.Reasoning*0.2) / 5.0
	default:
		quality = (c.Capabilities.Reasoning + c.Capabilities.Code + c.Capabilities.Creative) / 15.0
	}

	// Normalized Cost Component
	costNorm := math.Max(0.0, math.Log(c.Pricing.PromptPerMillionTokens+0.1)) / 4.0

	// Normalized Latency Component
	latSec := c.TTFTEMA / 1000.0
	if latSec < 0.05 {
		latSec = 0.05
	} else if latSec > 3.0 {
		latSec = 3.0
	}
	latencyNorm := latSec / 3.0

	// Weights based on tenant tier
	var wq, wc, wl float64
	switch req.TenantTier {
	case "enterprise":
		wq, wc, wl = 0.60, 0.15, 0.25
	case "free":
		wq, wc, wl = 0.30, 0.55, 0.15
	default: // "standard"
		wq, wc, wl = 0.50, 0.30, 0.20
	}

	baseFitness := (wq * quality) - (wc * costNorm) - (wl * latencyNorm)
	return baseFitness * c.HealthScore
}

func emergencyFallback(candidates []kvstore.CandidateModel, total int, start time.Time) (*RouteDecision, error) {
	var best *kvstore.CandidateModel
	for i := range candidates {
		c := &candidates[i]
		if c.HealthScore <= 0.05 {
			continue
		}
		if best == nil || c.Pricing.PromptPerMillionTokens < best.Pricing.PromptPerMillionTokens {
			best = c
		}
	}

	if best == nil {
		return nil, ErrAllProvidersDown
	}

	return &RouteDecision{
		Primary: SelectedRoute{
			ProviderID: best.ProviderID,
			ModelID:    best.ModelID,
			BaseURL:    best.BaseURL,
			Rank:       0,
			Score:      0.1,
			Reason:     "Emergency fallback: constraints relaxed, picked healthiest lowest cost model",
		},
		Fallbacks:       []SelectedRoute{},
		TotalEvaluated:  total,
		FilteredCount:   1,
		ExecutionMicros: time.Since(start).Microseconds(),
	}, nil
}
