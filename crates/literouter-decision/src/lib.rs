pub mod capability;
pub mod ffi;
pub mod pareto;
pub mod starlark_engine;

pub use capability::*;
pub use ffi::*;
pub use pareto::ParetoRouter;
pub use starlark_engine::StarlarkPolicyEngine;

#[cfg(test)]
mod tests {
    use super::*;

    fn sample_candidates() -> Vec<CandidateModel> {
        vec![
            CandidateModel {
                provider_id: "openai-direct".to_string(),
                model_id: "gpt-4o".to_string(),
                base_url: "https://api.openai.com/v1".to_string(),
                capabilities: CapabilityScores {
                    reasoning: 4.8,
                    code: 4.8,
                    math: 4.7,
                    creative: 4.9,
                    context_window: 128000,
                    latency_sensitivity: 4.0,
                },
                pricing: Pricing {
                    prompt_per_million: 2.50,
                    completion_per_million: 10.00,
                },
                health_score: 1.0,
                composite_cost: 2.5,
                ttft_ema_ms: 220.0,
            },
            CandidateModel {
                provider_id: "deepseek-api".to_string(),
                model_id: "deepseek-v3".to_string(),
                base_url: "https://api.deepseek.com/v1".to_string(),
                capabilities: CapabilityScores {
                    reasoning: 4.7,
                    code: 4.9,
                    math: 4.8,
                    creative: 4.2,
                    context_window: 64000,
                    latency_sensitivity: 4.5,
                },
                pricing: Pricing {
                    prompt_per_million: 0.14,
                    completion_per_million: 0.28,
                },
                health_score: 1.0,
                composite_cost: 0.8,
                ttft_ema_ms: 180.0,
            },
            CandidateModel {
                provider_id: "anthropic-direct".to_string(),
                model_id: "claude-3-5-sonnet".to_string(),
                base_url: "https://api.anthropic.com/v1".to_string(),
                capabilities: CapabilityScores {
                    reasoning: 4.9,
                    code: 5.0,
                    math: 4.8,
                    creative: 4.9,
                    context_window: 200000,
                    latency_sensitivity: 4.2,
                },
                pricing: Pricing {
                    prompt_per_million: 3.00,
                    completion_per_million: 15.00,
                },
                health_score: 1.0,
                composite_cost: 3.2,
                ttft_ema_ms: 260.0,
            },
        ]
    }

    #[test]
    fn test_code_routing_deepseek_cost_efficiency() {
        let candidates = sample_candidates();
        let req = RequestProfile {
            estimated_tokens: 4000,
            intent: IntentProfile::Code,
            min_code_score: Some(4.5),
            min_reasoning_score: None,
            max_cost_per_m: None,
            tenant_tier: Some("standard".to_string()),
            max_latency_ms: None,
        };

        let decision = ParetoRouter::route(&req, &candidates).expect("route failed");
        assert_eq!(decision.total_evaluated, 3);
        assert!(decision.filtered_count >= 1);
        // DeepSeek-V3 has top-tier code (4.9) at a fraction of cost ($0.14 vs $2.50) -> should win Pareto fitness
        assert_eq!(decision.primary.model_id, "deepseek-v3");
        assert!(!decision.fallbacks.is_empty());
    }

    #[test]
    fn test_enterprise_tier_prioritizes_top_quality() {
        let candidates = sample_candidates();
        let req = RequestProfile {
            estimated_tokens: 80000, // Exceeds DeepSeek's 64k window
            intent: IntentProfile::Code,
            min_code_score: Some(4.8),
            min_reasoning_score: None,
            max_cost_per_m: None,
            tenant_tier: Some("enterprise".to_string()),
            max_latency_ms: None,
        };

        let decision = ParetoRouter::route(&req, &candidates).expect("route failed");
        // DeepSeek filtered out because 80k > 64k context window!
        assert_ne!(decision.primary.model_id, "deepseek-v3");
        // Claude 3.5 Sonnet or GPT-4o must be selected
        assert!(
            decision.primary.model_id == "claude-3-5-sonnet"
                || decision.primary.model_id == "gpt-4o"
        );
    }

    #[test]
    fn test_starlark_override() {
        let candidates = sample_candidates();
        let req = RequestProfile {
            estimated_tokens: 1000,
            intent: IntentProfile::General,
            min_code_score: None,
            min_reasoning_score: None,
            max_cost_per_m: None,
            tenant_tier: Some("vip".to_string()),
            max_latency_ms: None,
        };

        let star_script = r#"
ROUTE_OVERRIDE = "claude-3-5-sonnet"
"#;
        let engine = StarlarkPolicyEngine::new(Some(star_script.to_string()));
        let override_opt = engine.evaluate_override(&req, &candidates);
        assert_eq!(override_opt.as_deref(), Some("claude-3-5-sonnet"));
    }
}
