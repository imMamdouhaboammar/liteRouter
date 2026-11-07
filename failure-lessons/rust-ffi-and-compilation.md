# Rust FFI and Compilation Failure Lessons

This document details failure classes, compiler contract shifts, and external crate lifecycle boundaries encountered in the mathematical decision engine (`crates/literouter-decision`).

---

## Rust 2024 Edition Unsafe Attribute Enforcement on C FFI Exports

### Context
In `crates/literouter-decision/src/ffi.rs`, the Pareto decision engine exposes C-ABI symbols (`literouter_decision_route`) for integration with the Go API gateway. Foreign-function export attributes traditionally relied on `#[no_mangle]`.

### What happened
During `cargo build`, the compiler rejected the standard `#[no_mangle]` attribute, failing compilation with a fatal syntax check error.

### Observable symptom
```
error: unsafe attribute used without unsafe
 --> src/ffi.rs:18:1
  |
18| #[no_mangle]
  | ^^^^^^^^^^^^
  |
  = help: use `#[unsafe(no_mangle)]` to acknowledge the unsafe nature of this attribute
```

### Impact
Build pipeline failure. Neither the Rust static/dynamic library nor the unified `liteRouter` gateway binary could be produced.

### Incorrect assumption
Assumed that legacy attribute syntax from Rust 2018/2021 editions (`#[no_mangle]`, `#[export_name]`) remains forward-compatible indefinitely.

### Root cause
**Confirmed**. In Rust 2024 edition (and recent compiler versions), attributes that subvert language safety guarantees (such as disabling symbol mangling, which can cause arbitrary symbol collisions across binary boundaries) require explicit acknowledgment via the `#[unsafe(...)]` attribute wrapper syntax.

### Why the architecture allowed it
The project configuration in `crates/literouter-decision/Cargo.toml` targets modern Rust toolchains, which strictly enforce attribute-level safety checks.

### Fix
Updated all C FFI export declarations in `crates/literouter-decision/src/ffi.rs` to wrap safety-critical attributes:

```rust
// Before (rejected in Rust 2024 edition)
#[no_mangle]
pub extern "C" fn literouter_decision_route(...) -> ...

// After (conforms to Rust 2024 edition specification)
#[unsafe(no_mangle)]
pub extern "C" fn literouter_decision_route(...) -> ...
```

### Verification
Re-ran `cargo check --manifest-path crates/literouter-decision/Cargo.toml` and `cargo test`. Verified zero compiler warnings or errors.

### Prevention rule
In Rust 2024+ codebases, all foreign symbol exports and linkage overrides must use `#[unsafe(no_mangle)]` or `#[unsafe(export_name = "...")]`. Never use bare unsafe attributes.

### Reusable lesson
Applies to all Rust CDylib/Staticlib crates, WebAssembly modules, and embedded FFI bridges interfacing with Go, Python, or C runtimes.

### Related code
- `crates/literouter-decision/src/ffi.rs`: `literouter_decision_route`, `literouter_free_string`.

### Related tests
- `make build` and `crates/literouter-decision/Cargo.toml` build pipeline.

### Related lessons
- [concurrency-and-lifecycle.md](concurrency-and-lifecycle.md#go-subscription-notification-channel-double-close-panic)

### Status
**Resolved**.

---

## Starlark 0.14+ Breaking API Evolution: Module Heap and Lookup Semantics

### Context
In `crates/literouter-decision/src/starlark_policy.rs`, Meta's official `starlark` Rust crate is embedded to evaluate dynamic `.star` routing policies without recompiling the gateway.

### What happened
Implementation code attempting to construct Starlark execution environments using `Module::new()` failed compilation. Additionally, extracting globals via `module.get(...)` caused type mismatch errors when treating the return value as a `Result`.

### Observable symptom
```
error[E0599]: no function or associated item named `new` found for struct `starlark::environment::Module` in the current scope
  --> src/starlark_policy.rs:24:28
   |
24 |         let module = Module::new();
   |                              ^^^ function or associated item not found in `Module`

error[E0308]: mismatched types: expected `Result<_, _>`, found `Option<Value<'_>>`
  --> src/starlark_policy.rs:36:20
```

### Impact
Failed build for dynamic policy evaluation, disabling user-configurable tenant rules.

### Incorrect assumption
Assumed that `starlark-rust` 0.14 retained the standalone constructor `Module::new()` and `Result`-based symbol lookup semantics from earlier 0.9/0.10 releases.

### Root cause
**Confirmed**. In `starlark-rust` 0.14+, `Module` lifecycle is tied to an explicit heap allocation scope. Modules cannot be constructed standalone; they must be evaluated within `Module::with_temp_heap(|module| { ... })`. Furthermore, `module.get(...)` returns `Option<Value>`, where `None` indicates symbol absence rather than a runtime error.

### Why the architecture allowed it
Relying on statistical training memory or outdated examples rather than inspecting the actively installed crate documentation and type signatures.

### Fix
Refactored `StarlarkPolicyEngine::evaluate` to use closure-scoped temporary heap execution and idiomatic `Option` handling:

```rust
// Scoped temporary heap execution in Starlark 0.14
Module::with_temp_heap(|module| {
    let mut eval = Evaluator::new(module);
    eval.eval_module(ast, &globals)?;

    // Handle Option return type from module.get()
    if let Some(override_val) = module.get("ROUTE_OVERRIDE") {
        let val_str = override_val.unpack_str().unwrap_or_default();
        if !val_str.is_empty() {
            return Ok(Some(val_str.to_string()));
        }
    }
    Ok(None)
})
```

### Verification
Added automated unit test `tests::test_starlark_override` in `crates/literouter-decision/src/lib.rs`. Tested dynamic policy execution with custom tenant rules. Verified passing test with `cargo test`.

### Prevention rule
When integrating third-party language runtimes or AST evaluators, always inspect the installed crate's API interface via `cargo doc` or source analysis rather than assuming constructor signatures. Ensure heap lifetimes are strictly scoped.

### Reusable lesson
Applies to all embedded scripting engines (Starlark, V8, Wasmer, Wasmtime, Lua). Heap allocation must be scoped to request boundaries to prevent unbounded growth.

### Related code
- `crates/literouter-decision/src/starlark_policy.rs`: `StarlarkPolicyEngine`.
- `rules/default_routing.star`: Declarative routing rules.

### Related tests
- `crates/literouter-decision/src/lib.rs`: `tests::test_starlark_override`.

### Related lessons
- [gateway-and-fault-isolation.md](gateway-and-fault-isolation.md#transparent-multi-tier-failover-with-zero-client-facing-outage)

### Status
**Resolved**.
