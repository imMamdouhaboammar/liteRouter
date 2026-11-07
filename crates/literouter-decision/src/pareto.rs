use crate::capability::{
    CandidateModel, IntentProfile, RequestProfile, RouteDecision, SelectedRoute,
};
use std::time::Instant;

pub struct ParetoRouter;

impl ParetoRouter {
    pub fn route(
        req: &RequestProfile,
        candidates: &[CandidateModel],
    ) -> Result<RouteDecision, String> {
        let start = Instant::now();
        let total_evaluated = candidates.len();

        if candidates.is_empty() {
            return Err("No candidate models provided to decision engine".to_string());
        }

        // Step 1: Filter hard constraints
        let mut eligible: Vec<(&CandidateModel, f64)> = candidates
            .iter()
            .filter_map(|c| {
                // Must be alive
                if c.health_score <= 0.05 {
                    return None;
                }

                // Context window constraint
                if c.capabilities.context_window < req.estimated_tokens {
                    return None;
                }

                // Capability constraints
                if req
                    .min_code_score
                    .is_some_and(|min| c.capabilities.code < min)
                {
                    return None;
                }

                if req
                    .min_reasoning_score
                    .is_some_and(|min| c.capabilities.reasoning < min)
                {
                    return None;
                }

                // Cost budget constraint
                if req
                    .max_cost_per_m
                    .is_some_and(|max| c.pricing.prompt_per_million > max)
                {
                    return None;
                }

                // Latency constraint
                if req.max_latency_ms.is_some_and(|max| c.ttft_ema_ms > max) {
                    return None;
                }

                // Step 2: Compute Multi-Objective Fitness
                let fitness = Self::calculate_fitness(req, c);
                Some((c, fitness))
            })
            .collect();

        if eligible.is_empty() {
            // Fallback: If no candidate satisfies strict constraints, relax constraints and pick healthiest lowest cost
            return Self::emergency_fallback(candidates, start.elapsed().as_micros() as u64);
        }

        // Sort descending by fitness score (highest fitness wins)
        eligible.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        let filtered_count = eligible.len();
        let primary_cand = eligible[0].0;
        let primary_score = eligible[0].1;

        let primary = SelectedRoute {
            provider_id: primary_cand.provider_id.clone(),
            model_id: primary_cand.model_id.clone(),
            base_url: primary_cand.base_url.clone(),
            rank: 0,
            score: primary_score,
            reason: format!(
                "Optimal Pareto route: intent={:?}, health={:.2}, cost=${:.2}/M, ttft={:.1}ms",
                req.intent,
                primary_cand.health_score,
                primary_cand.pricing.prompt_per_million,
                primary_cand.ttft_ema_ms
            ),
        };

        let fallbacks = eligible
            .iter()
            .skip(1)
            .take(3)
            .enumerate()
            .map(|(idx, (cand, score))| SelectedRoute {
                provider_id: cand.provider_id.clone(),
                model_id: cand.model_id.clone(),
                base_url: cand.base_url.clone(),
                rank: idx + 1,
                score: *score,
                reason: format!(
                    "Fallback rank {}: health={:.2}, cost=${:.2}/M, ttft={:.1}ms",
                    idx + 1,
                    cand.health_score,
                    cand.pricing.prompt_per_million,
                    cand.ttft_ema_ms
                ),
            })
            .collect();

        Ok(RouteDecision {
            primary,
            fallbacks,
            total_evaluated,
            filtered_count,
            execution_micros: start.elapsed().as_micros() as u64,
        })
    }

    fn calculate_fitness(req: &RequestProfile, c: &CandidateModel) -> f64 {
        // Intent Quality Component (Normalized 0.0 to 1.0)
        let quality = match req.intent {
            IntentProfile::Code => {
                (c.capabilities.code * 0.7 + c.capabilities.reasoning * 0.3) / 5.0
            }
            IntentProfile::Reasoning | IntentProfile::Math => {
                (c.capabilities.reasoning * 0.6 + c.capabilities.math * 0.4) / 5.0
            }
            IntentProfile::Creative => {
                (c.capabilities.creative * 0.8 + c.capabilities.reasoning * 0.2) / 5.0
            }
            IntentProfile::General | IntentProfile::Embedding => {
                (c.capabilities.reasoning + c.capabilities.code + c.capabilities.creative) / 15.0
            }
        };

        // Cost Component ($0.1 to $30.0 / 1M prompt tokens)
        // Normalized logarithmically to avoid skew from ultra-expensive models
        let cost_norm = (c.pricing.prompt_per_million + 0.1).ln().max(0.0) / 4.0;

        // Latency Component (50ms to 2000ms)
        let latency_norm = (c.ttft_ema_ms / 1000.0).clamp(0.05, 3.0) / 3.0;

        // Weights: Higher quality is prioritized, then low cost, then low latency
        let (wq, wc, wl) = match req.tenant_tier.as_deref() {
            Some("enterprise") => (0.60, 0.15, 0.25),
            Some("free") => (0.30, 0.55, 0.15),
            _ => (0.50, 0.30, 0.20), // Standard tier
        };

        let base_fitness = (wq * quality) - (wc * cost_norm) - (wl * latency_norm);

        // Multiplied by health score (e.g. 0.5 if degraded, 1.0 if healthy)
        base_fitness * c.health_score
    }

    fn emergency_fallback(
        candidates: &[CandidateModel],
        micros: u64,
    ) -> Result<RouteDecision, String> {
        let best_fallback = candidates
            .iter()
            .filter(|c| c.health_score > 0.0)
            .min_by(|a, b| {
                a.pricing
                    .prompt_per_million
                    .partial_cmp(&b.pricing.prompt_per_million)
                    .unwrap_or(std::cmp::Ordering::Equal)
            })
            .ok_or_else(|| {
                "All candidate providers are currently offline (Status Down)".to_string()
            })?;

        Ok(RouteDecision {
            primary: SelectedRoute {
                provider_id: best_fallback.provider_id.clone(),
                model_id: best_fallback.model_id.clone(),
                base_url: best_fallback.base_url.clone(),
                rank: 0,
                score: 0.1,
                reason:
                    "Emergency fallback: constraints relaxed, picked healthiest lowest cost model"
                        .to_string(),
            },
            fallbacks: vec![],
            total_evaluated: candidates.len(),
            filtered_count: 1,
            execution_micros: micros,
        })
    }
}
