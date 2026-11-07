# Lessons Index

This index indexes every documented failure class in `failure-lessons/`, its prevention invariant, and its status.

---

## Registry Table

| Lesson | Failure Class | Prevention Rule | System | Status | Document |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Go Subscription Double-Close Panic** | Concurrency Race / Channel Lifecycle | Subscription channels must be wrapped in an idempotent close guard (`sync.Once`). | `pkg/kvstore` | Resolved | [concurrency-and-lifecycle.md](concurrency-and-lifecycle.md#go-subscription-notification-channel-double-close-panic) |
| **Rust 2024 Unsafe FFI Attributes** | Compiler Contract Drift | Wrap FFI symbol export attributes in `#[unsafe(no_mangle)]` for Rust 2024 edition compliance. | `crates/literouter-decision` | Resolved | [rust-ffi-and-compilation.md](rust-ffi-and-compilation.md#rust-2024-edition-unsafe-attribute-enforcement-on-c-ffi-exports) |
| **Starlark 0.14+ Module Lifecycle Drift** | Library API Evolution | Allocate Starlark evaluation environments via `Module::with_temp_heap` closure and handle `Option` lookups. | `crates/literouter-decision` | Resolved | [rust-ffi-and-compilation.md](rust-ffi-and-compilation.md#starlark-014-breaking-api-evolution-module-heap-and-lookup-semantics) |
| **Upstream Error Classification Blindness** | Fault Isolation Boundary | Classify HTTP responses into retryable outages (5xx/429) vs client/auth errors (401/403/400); do not trip breakers on caller auth faults. | `pkg/circuitbreaker`, `pkg/gateway` | Resolved | [gateway-and-fault-isolation.md](gateway-and-fault-isolation.md#upstream-error-classification-and-circuit-breaker-discrimination) |
| **Transparent Multi-Tier Failover** | Resilience & Availability | Upstream route failures must fail over across pre-ranked candidate models (R0 -> R1 -> R2) before returning 502/503. | `pkg/circuitbreaker`, `pkg/gateway` | Resolved | [gateway-and-fault-isolation.md](gateway-and-fault-isolation.md#transparent-multi-tier-failover-with-zero-client-facing-outage) |
| **Documentation Buzzword Inflation & Slop** | Communication Integrity | Adhere strictly to the Direct Payload Invariant: transition from domain headers directly into technical payloads with zero conversational preambles. | `README.md` | Resolved | [documentation-and-slop-prevention.md](documentation-and-slop-prevention.md#promotional-fluff-and-preamble-slop-in-technical-documentation) |

---

## Rules We Now Enforce

1. **Idempotent Channel Teardown Invariant:** Any Go channel exposed to asynchronous subscribers must never be closed directly via `close(ch)`. It must be encapsulated inside an unexported wrapper struct paired with `sync.Once`.
2. **Rust 2024 FFI Quarantine Invariant:** All exported C FFI symbols in the decision engine must explicitly declare `#[unsafe(no_mangle)]` to satisfy compiler safety contracts.
3. **Starlark Temporary Heap Invariant:** All embedded Starlark script evaluations must occur within scoped temporary heaps (`Module::with_temp_heap`) to prevent unmanaged memory leaks across execution requests.
4. **Error Discrimination Invariant:** Client credential errors (HTTP 401/403) and bad requests (HTTP 400) must never increment circuit breaker failure counters or alter health metrics. Only server errors (5xx), network timeouts, and rate limits (429) alter adjacency states.
5. **Direct Payload Documentation Invariant:** Technical documentation must eliminate conversational preambles, affordance narration, and promotional buzzwords. All sections must lead with technical specifications, tables, or verified code snippets.
6. **Executable Example Invariant:** Every code block, command line, or curl request in repository documentation must run verbatim against the current build without modification or failure.
