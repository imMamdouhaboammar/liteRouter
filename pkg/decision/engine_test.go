package decision

import (
	"testing"

	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
)

func sampleCandidates() []kvstore.CandidateModel {
	return []kvstore.CandidateModel{
		{
			ProviderID: "openai-direct",
			ModelID:    "gpt-4o",
			BaseURL:    "https://api.openai.com/v1",
			Capabilities: kvstore.CapabilityScores{
				Reasoning:     4.8,
				Code:          4.8,
				Math:          4.7,
				Creative:      4.9,
				ContextWindow: 128000,
			},
			Pricing: kvstore.Pricing{
				PromptPerMillionTokens:     2.50,
				CompletionPerMillionTokens: 10.00,
			},
			HealthScore:   1.0,
			CompositeCost: 2.5,
			TTFTEMA:       220.0,
		},
		{
			ProviderID: "deepseek-api",
			ModelID:    "deepseek-v3",
			BaseURL:    "https://api.deepseek.com/v1",
			Capabilities: kvstore.CapabilityScores{
				Reasoning:     4.7,
				Code:          4.9,
				Math:          4.8,
				Creative:      4.2,
				ContextWindow: 64000,
			},
			Pricing: kvstore.Pricing{
				PromptPerMillionTokens:     0.14,
				CompletionPerMillionTokens: 0.28,
			},
			HealthScore:   1.0,
			CompositeCost: 0.8,
			TTFTEMA:       180.0,
		},
		{
			ProviderID: "anthropic-direct",
			ModelID:    "claude-3-5-sonnet",
			BaseURL:    "https://api.anthropic.com/v1",
			Capabilities: kvstore.CapabilityScores{
				Reasoning:     4.9,
				Code:          5.0,
				Math:          4.8,
				Creative:      4.9,
				ContextWindow: 200000,
			},
			Pricing: kvstore.Pricing{
				PromptPerMillionTokens:     3.00,
				CompletionPerMillionTokens: 15.00,
			},
			HealthScore:   1.0,
			CompositeCost: 3.2,
			TTFTEMA:       260.0,
		},
	}
}

func TestClassifyIntent(t *testing.T) {
	tests := []struct {
		prompt string
		want   IntentProfile
	}{
		{"Write a python function `def fib(n):`", IntentCode},
		{"Calculate the integral of x^2 from 0 to 1", IntentMath},
		{"Analyze why does the system fail and explain step-by-step", IntentReasoning},
		{"Write a creative poem about space exploration", IntentCreative},
		{"Hello, what is the weather today?", IntentGeneral},
	}

	for _, tc := range tests {
		got := ClassifyIntent(tc.prompt)
		if got != tc.want {
			t.Errorf("prompt '%s': got intent %s, want %s", tc.prompt, got, tc.want)
		}
	}
}

func TestDecisionEngineParetoCodeRouting(t *testing.T) {
	candidates := sampleCandidates()
	engine := NewEngine()

	req := RequestProfile{
		EstimatedTokens: 4000,
		Intent:          IntentCode,
		MinCodeScore:    4.5,
		TenantTier:      "standard",
	}

	decision, err := engine.Route(req, candidates)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	if decision.TotalEvaluated != 3 {
		t.Fatalf("expected 3 evaluated, got %d", decision.TotalEvaluated)
	}

	// For standard tier code, DeepSeek-V3 offers 4.9 code score at $0.14/M vs $2.50/$3.00 -> should win Pareto fitness
	if decision.Primary.ModelID != "deepseek-v3" {
		t.Fatalf("expected primary route deepseek-v3, got %s", decision.Primary.ModelID)
	}

	if len(decision.Fallbacks) == 0 {
		t.Fatal("expected fallback routes to be computed")
	}
}

func TestDecisionEngineContextWindowConstraint(t *testing.T) {
	candidates := sampleCandidates()
	engine := NewEngine()

	req := RequestProfile{
		EstimatedTokens: 80000, // Exceeds DeepSeek-V3's 64k limit!
		Intent:          IntentCode,
		TenantTier:      "enterprise",
	}

	decision, err := engine.Route(req, candidates)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	// DeepSeek-V3 must NOT be selected because context_window 64000 < 80000
	if decision.Primary.ModelID == "deepseek-v3" {
		t.Fatal("expected deepseek-v3 to be filtered out due to context window constraint")
	}

	// Must be claude-3-5-sonnet (200k) or gpt-4o (128k)
	if decision.Primary.ModelID != "claude-3-5-sonnet" && decision.Primary.ModelID != "gpt-4o" {
		t.Fatalf("unexpected primary model: %s", decision.Primary.ModelID)
	}
}

func TestDecisionEngineStarlarkPolicyOverride(t *testing.T) {
	candidates := sampleCandidates()
	engine := NewEngine()

	script := `
# Custom Starlark Routing Override
ROUTE_OVERRIDE = "claude-3-5-sonnet"
`
	engine.SetPolicyScript(script)

	req := RequestProfile{
		EstimatedTokens: 1000,
		Intent:          IntentGeneral,
		TenantTier:      "free",
	}

	decision, err := engine.Route(req, candidates)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	if decision.Primary.ModelID != "claude-3-5-sonnet" {
		t.Fatalf("expected Starlark policy override to claude-3-5-sonnet, got %s", decision.Primary.ModelID)
	}
}
