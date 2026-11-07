use crate::capability::{CandidateModel, RequestProfile};
use starlark::environment::{Globals, Module};
use starlark::eval::Evaluator;
use starlark::syntax::{AstModule, Dialect};

pub struct StarlarkPolicyEngine {
    script: Option<String>,
}

impl StarlarkPolicyEngine {
    pub fn new(script: Option<String>) -> Self {
        Self { script }
    }

    pub fn set_script(&mut self, script: String) {
        self.script = Some(script);
    }

    /// Evaluates the Starlark script if present.
    /// Returns Some(model_override) if the script decides on a deterministic route override.
    pub fn evaluate_override(
        &self,
        req: &RequestProfile,
        candidates: &[CandidateModel],
    ) -> Option<String> {
        let script = self.script.as_ref()?;
        let ast = AstModule::parse("policy.star", script.clone(), &Dialect::Standard).ok()?;
        let globals = Globals::standard();

        Module::with_temp_heap(|module| {
            // Inject request parameters into module
            let intent_str = format!("{:?}", req.intent).to_lowercase();
            let tenant_str = req
                .tenant_tier
                .clone()
                .unwrap_or_else(|| "standard".to_string());

            let req_json = serde_json::json!({
                "estimated_tokens": req.estimated_tokens,
                "intent": intent_str,
                "tenant_tier": tenant_str,
            });

            let cand_json = serde_json::to_value(candidates).unwrap_or_default();

            let setup_code = format!(
                "req_raw = '{}'\ncandidates_raw = '{}'\n",
                req_json.to_string().replace('\'', "\\'"),
                cand_json.to_string().replace('\'', "\\'")
            );

            if let Ok(setup_ast) = AstModule::parse("setup.star", setup_code, &Dialect::Standard) {
                let mut eval = Evaluator::new(&module);
                let _ = eval.eval_module(setup_ast, &globals);
            }

            let mut eval = Evaluator::new(&module);
            let res = eval.eval_module(ast, &globals).ok();

            // Check global variable 'ROUTE_OVERRIDE'
            if let Some(s) = module
                .get("ROUTE_OVERRIDE")
                .and_then(|v| v.unpack_str())
                .filter(|s| !s.is_empty())
            {
                return Some(s.to_string());
            }

            // Alternatively check return value of evaluated script
            if let Some(s) = res.and_then(|v| v.unpack_str()).filter(|s| !s.is_empty()) {
                return Some(s.to_string());
            }

            None
        })
    }
}
