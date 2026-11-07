use crate::capability::{CandidateModel, RequestProfile, RouteDecision, SelectedRoute};
use crate::pareto::ParetoRouter;
use crate::starlark_engine::StarlarkPolicyEngine;
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

/// Computes the optimal Pareto route for a request JSON against candidate models JSON.
///
/// # Safety
///
/// Callers must ensure `req_json_ptr` and `cand_json_ptr` point to valid, null-terminated
/// C strings or null pointers. The returned pointer must be deallocated using
/// `literouter_decision_free_string`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn literouter_decision_route_json(
    req_json_ptr: *const c_char,
    cand_json_ptr: *const c_char,
    policy_star_ptr: *const c_char,
) -> *mut c_char {
    if req_json_ptr.is_null() || cand_json_ptr.is_null() {
        return CString::new(r#"{"error":"Null pointer passed to decision engine"}"#)
            .unwrap()
            .into_raw();
    }

    let req_str = match unsafe { CStr::from_ptr(req_json_ptr) }.to_str() {
        Ok(s) => s,
        Err(e) => {
            return CString::new(format!(r#"{{"error":"Invalid UTF-8 in request: {}"}}"#, e))
                .unwrap()
                .into_raw();
        }
    };

    let cand_str = match unsafe { CStr::from_ptr(cand_json_ptr) }.to_str() {
        Ok(s) => s,
        Err(e) => {
            return CString::new(format!(
                r#"{{"error":"Invalid UTF-8 in candidates: {}"}}"#,
                e
            ))
            .unwrap()
            .into_raw();
        }
    };

    let policy_script = if !policy_star_ptr.is_null() {
        unsafe { CStr::from_ptr(policy_star_ptr) }
            .to_str()
            .ok()
            .map(|s| s.to_string())
    } else {
        None
    };

    let req: RequestProfile = match serde_json::from_str(req_str) {
        Ok(r) => r,
        Err(e) => {
            return CString::new(format!(
                r#"{{"error":"Failed to parse request JSON: {}"}}"#,
                e
            ))
            .unwrap()
            .into_raw();
        }
    };

    let candidates: Vec<CandidateModel> = match serde_json::from_str(cand_str) {
        Ok(c) => c,
        Err(e) => {
            return CString::new(format!(
                r#"{{"error":"Failed to parse candidates JSON: {}"}}"#,
                e
            ))
            .unwrap()
            .into_raw();
        }
    };

    // Evaluate dynamic Starlark policy override if available
    if let Some(script) = policy_script {
        let engine = StarlarkPolicyEngine::new(Some(script));
        if let Some(override_model) = engine.evaluate_override(&req, &candidates) {
            // Find candidate matching override
            if let Some(cand) = candidates.iter().find(|c| c.model_id == override_model) {
                let decision = RouteDecision {
                    primary: SelectedRoute {
                        provider_id: cand.provider_id.clone(),
                        model_id: cand.model_id.clone(),
                        base_url: cand.base_url.clone(),
                        rank: 0,
                        score: 1.0,
                        reason: "Selected by dynamic Starlark routing policy".to_string(),
                    },
                    fallbacks: vec![],
                    total_evaluated: candidates.len(),
                    filtered_count: 1,
                    execution_micros: 5,
                };
                let res_json = serde_json::to_string(&decision).unwrap_or_default();
                return CString::new(res_json).unwrap().into_raw();
            }
        }
    }

    // Default: Multi-Objective Pareto SPF Router
    match ParetoRouter::route(&req, &candidates) {
        Ok(decision) => {
            let res_json = serde_json::to_string(&decision).unwrap_or_default();
            CString::new(res_json).unwrap().into_raw()
        }
        Err(err) => CString::new(format!(r#"{{"error":"{}"}}"#, err))
            .unwrap()
            .into_raw(),
    }
}

/// Deallocates a C string allocated by `literouter_decision_route_json`.
///
/// # Safety
///
/// `ptr` must be a pointer returned from `literouter_decision_route_json` or a null pointer.
/// Calling this multiple times on the same non-null pointer invokes undefined behavior.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn literouter_decision_free_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        drop(unsafe { CString::from_raw(ptr) });
    }
}
