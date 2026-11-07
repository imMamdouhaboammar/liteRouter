# liteRouter Agent Constitution & Engineering Instructions

This document governs architecture standards, non-negotiable invariants, and development workflows for `liteRouter`.

---

## 1. Project Overview & Architecture

`liteRouter` is a distributed AI model routing engine inspired by Meta's OpenR:
- **Go Fabric (`pkg/`):** High-concurrency L7 HTTP/SSE API Gateway, Link-State `KvStore` with 3-way synchronization, and `Spark` background health prober.
- **Rust Decision Core (`crates/literouter-decision`):** Sub-millisecond multi-objective Pareto SPF routing engine and embedded Starlark policy evaluator (`starlark-rust` 0.14+).

---

## 2. Failure Lessons & Project Memory

All engineering failures, root-cause analyses, regression mappings, and falsification proofs are durably maintained in [`failure-lessons/`](failure-lessons/README.md).

For full architectural postmortems and details, see:
- [Lessons Index & Status](failure-lessons/lessons-index.md)
- [Testing & Verification Proofs](failure-lessons/testing-and-verification.md)
- [Concurrency & Channel Lifecycle](failure-lessons/concurrency-and-lifecycle.md)
- [Rust FFI & Starlark Compilation](failure-lessons/rust-ffi-and-compilation.md)
- [Gateway Fault Isolation & Failover](failure-lessons/gateway-and-fault-isolation.md)
- [Documentation & Anti-Slop Prevention](failure-lessons/documentation-and-slop-prevention.md)

---

## 3. Rules We Now Enforce (Session Invariants)

Future contributors and AI agents must preserve the following non-negotiable invariants:

1. **Idempotent Channel Teardown Invariant:** Any Go channel exposed to asynchronous subscribers must never be closed directly via bare `close(ch)`. It must be encapsulated inside a dedicated wrapper struct governed by `sync.Once`.
   - *Details:* [`failure-lessons/concurrency-and-lifecycle.md`](failure-lessons/concurrency-and-lifecycle.md)
2. **Rust 2024 FFI Quarantine Invariant:** All exported C FFI symbols in the decision engine must declare `#[unsafe(no_mangle)]` to satisfy compiler safety contracts.
   - *Details:* [`failure-lessons/rust-ffi-and-compilation.md`](failure-lessons/rust-ffi-and-compilation.md)
3. **Starlark Temporary Heap Invariant:** All embedded Starlark script evaluations must execute within scoped temporary heaps (`Module::with_temp_heap`) and handle `Option` return types from `module.get()`.
   - *Details:* [`failure-lessons/rust-ffi-and-compilation.md`](failure-lessons/rust-ffi-and-compilation.md)
4. **Error Discrimination Invariant:** Client credential errors (HTTP 401/403) and validation errors (HTTP 400) must never trip circuit breakers, trigger failover, or alter provider health scores. Only upstream outages (5xx, 429, connection timeouts) degrade health metrics.
   - *Details:* [`failure-lessons/gateway-and-fault-isolation.md`](failure-lessons/gateway-and-fault-isolation.md)
5. **Transparent Fallback Invariant:** Every routing calculation must produce an ordered fallback chain (R0, R1, R2). The gateway must transparently fail over across healthy alternatives before returning 502/503 errors to the client.
   - *Details:* [`failure-lessons/gateway-and-fault-isolation.md`](failure-lessons/gateway-and-fault-isolation.md)
6. **Direct Payload Documentation Invariant:** Technical documentation must eliminate conversational preambles, affordance narration, and promotional buzzwords. All sections must lead with technical specifications, tables, or verified code snippets.
   - *Details:* [`failure-lessons/documentation-and-slop-prevention.md`](failure-lessons/documentation-and-slop-prevention.md)
7. **Executable Code Example Invariant:** Every command line, curl example, or code block in repository documentation must run verbatim against the active build without modification or failure.

---

## 4. Verification Workflow

Before proposing code changes or completing turns:
1. Run `make test` to execute both Rust unit tests and Go tests with race detection (`go test -v -count=1 -race ./...`).
2. Verify that any new failure class is documented in `failure-lessons/` with a corresponding regression test.

---

- Unsorted learnings from earlier sessions, read them before starting work: @docs/session-learnings/AGENTS.md
