use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IntentProfile {
    General,
    Code,
    Reasoning,
    Math,
    Creative,
    Embedding,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CapabilityScores {
    pub reasoning: f64,
    pub code: f64,
    pub math: f64,
    pub creative: f64,
    pub context_window: usize,
    pub latency_sensitivity: f64,
}

impl Default for CapabilityScores {
    fn default() -> Self {
        Self {
            reasoning: 3.0,
            code: 3.0,
            math: 3.0,
            creative: 3.0,
            context_window: 8192,
            latency_sensitivity: 3.0,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Pricing {
    pub prompt_per_million: f64,
    pub completion_per_million: f64,
}

impl Default for Pricing {
    fn default() -> Self {
        Self {
            prompt_per_million: 1.0,
            completion_per_million: 2.0,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CandidateModel {
    pub provider_id: String,
    pub model_id: String,
    pub base_url: String,
    pub capabilities: CapabilityScores,
    pub pricing: Pricing,
    pub health_score: f64, // 0.0 to 1.0
    pub composite_cost: f64,
    pub ttft_ema_ms: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RequestProfile {
    pub estimated_tokens: usize,
    pub intent: IntentProfile,
    pub min_code_score: Option<f64>,
    pub min_reasoning_score: Option<f64>,
    pub max_cost_per_m: Option<f64>,
    pub tenant_tier: Option<String>,
    pub max_latency_ms: Option<f64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SelectedRoute {
    pub provider_id: String,
    pub model_id: String,
    pub base_url: String,
    pub rank: usize,
    pub score: f64,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RouteDecision {
    pub primary: SelectedRoute,
    pub fallbacks: Vec<SelectedRoute>,
    pub total_evaluated: usize,
    pub filtered_count: usize,
    pub execution_micros: u64,
}
