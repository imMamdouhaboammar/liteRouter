# Testing and Verification

This document establishes the bidirectional mapping between documented failure classes, automated regression tests, and protected invariants.

---

## Failure-to-Test Mappings

$$\text{Failure Class} \longrightarrow \text{Regression Test} \longrightarrow \text{Protected Invariant}$$

| Failure Class | Regression Test File | Test Symbol / Command | Protected Invariant |
| :--- | :--- | :--- | :--- |
| **Go Subscription Double-Close Panic** | `pkg/kvstore/kvstore_test.go` | `TestKvStoreSubscribeDelta` | Closing KvStore with active subscribers executes without panic. |
| **Rust 2024 Unsafe FFI Attributes** | `crates/literouter-decision/Cargo.toml` | `cargo check --manifest-path crates/literouter-decision/Cargo.toml` | All exported C-ABI symbols compile cleanly under Rust 2024 checks. |
| **Starlark Module Lifecycle Drift** | `crates/literouter-decision/src/lib.rs` | `tests::test_starlark_override` | Custom Starlark script evaluates dynamically in isolated heap. |
| **Circuit Breaker 429 Trip** | `pkg/circuitbreaker/circuit_breaker_test.go` | `TestCircuitBreaker429ImmediateTrip` | Upstream HTTP 429 immediately transitions breaker to `StateOpen`. |
| **Transparent Route Failover** | `pkg/circuitbreaker/circuit_breaker_test.go` | `TestFailoverCoordinatorTransparentFallback` | Request transparently succeeds on fallback model when primary returns 500. |
| **Pareto Intent Routing** | `pkg/decision/decision_test.go` | `TestDecisionEngineParetoCodeRouting` | Code prompts select highest code capability model with optimal cost. |
| **Gateway Telemetry & Header Injection** | `pkg/gateway/gateway_test.go` | `TestGatewayChatCompletionsRouting` | Telemetry headers (`X-LiteRouter-*`) are injected into client responses. |

---

## 3-Step Falsification Proofs

A regression test is valid only if proven to fail when the defect is reintroduced:

### 1. Subscription Double-Close Falsification
1. **Fix Applied:** `subscription.close()` uses `s.once.Do(func() { close(s.ch) })`. Run `go test -v -race ./pkg/kvstore`. Result: `PASS`.
2. **Defect Injected:** Replace `s.once.Do(...)` with bare `close(s.ch)` and invoke both `unsubscribe()` and `kv.Close()`. Result: `panic: close of closed channel` (Test fails, proving sensitivity).
3. **Fix Restored:** Restore `sync.Once` wrapper. Result: `PASS`.

### 2. Rust 2024 FFI Attribute Falsification
1. **Fix Applied:** `#[unsafe(no_mangle)]` on `literouter_decision_route`. Run `cargo check`. Result: `Finished dev profile ok`.
2. **Defect Injected:** Replace with bare `#[no_mangle]`. Result: Compiler error: `unsafe attribute used without unsafe` (Compiler fails, proving contract enforcement).
3. **Fix Restored:** Restore `#[unsafe(no_mangle)]`. Result: `Finished dev profile ok`.

### 3. Circuit Breaker Rate Limit Falsification
1. **Fix Applied:** `RecordFailure(429)` sets `StateOpen` immediately. Run `go test -v -run TestCircuitBreaker429ImmediateTrip ./pkg/circuitbreaker`. Result: `PASS`.
2. **Defect Injected:** Change `RecordFailure` to treat 429 as a standard consecutive error increment (`consecutiveFails++` up to threshold 3). Run test. Result: `FAIL: Expected state open immediately after single 429, got closed`.
3. **Fix Restored:** Restore immediate 429 trip logic. Result: `PASS`.

---

## Verification Commands

Execute the complete verification pipeline with data race detection:

```bash
make test
```

Verifies:
- Rust crate compilation and 3 Pareto/Starlark tests.
- Go packages under `-race` detector (zero race conditions across concurrent goroutines).
