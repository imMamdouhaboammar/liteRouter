# Gateway and Fault Isolation Failure Lessons

This document details failure classes and engineering invariants related to reverse proxy execution, upstream error discrimination, and transparent fallback routing.

---

## Upstream Error Classification and Circuit Breaker Discrimination

### Context
In `pkg/circuitbreaker/circuit_breaker.go` and `pkg/gateway/gateway.go`, the gateway intercepts upstream HTTP responses from AI provider endpoints. The circuit breaker must protect the cluster against flaky or degraded upstreams while avoiding false-positive outages.

### What happened
During end-to-end testing with unconfigured or bad provider credentials, the upstream returned `HTTP 401 Unauthorized`. In a naive circuit breaker implementation, any non-2xx status code increments the failure counter, causing the breaker to open and fail over to alternate providers, masking client authentication faults.

### Observable symptom
Client receives unexpected failover across multiple providers, or healthy upstream providers are prematurely marked down because a caller provided an invalid API key.

### Impact
1. Masking client configuration bugs as infrastructure outages.
2. Thundering-herd failover where an invalid client request sequentially trips breakers across all available upstream providers, taking down the entire routing pool.

### Incorrect assumption
Assumed that any HTTP error ($status \ge 400$) constitutes an upstream provider failure.

### Root cause
**Confirmed**. Failure to discriminate between upstream availability/capacity faults ($5xx$, $429$, connection reset, read timeout) and client-induced request errors ($400$ Bad Request, $401$ Unauthorized, $403$ Forbidden). Caller mistakes are not infrastructure outages.

### Why the architecture allowed it
The failure reporting seam in `circuitbreaker.RecordFailure(statusCode)` needed strict categorization criteria distinguishing upstream link failures from application payload rejection.

### Fix
Implemented strict HTTP status code classification:
- **Immediate Breaker Trip (`StateOpen` with Exponential Backoff):** `HTTP 429 Too Many Requests`. Sets cooldown to 5 seconds, doubling on repeated 429s up to 60 seconds.
- **Consecutive Error Trip:** `HTTP 500, 502, 503, 504` and network I/O errors. Tripping requires 3 consecutive failures.
- **Pass-Through (No Breaker Penalty):** `HTTP 400, 401, 403, 404, 422`. These errors are returned directly to the caller with telemetry headers intact (`X-LiteRouter-Provider`, `X-LiteRouter-Attempts`), without incrementing failure counters.

### Verification
Simulated `HTTP 401 Unauthorized` via curl against `./bin/literouter`. Verified that the gateway passes the error through transparently without altering the provider's health score in `KvStore` or opening the circuit breaker. Verified `TestCircuitBreaker429ImmediateTrip` in `pkg/circuitbreaker/circuit_breaker_test.go`.

### Prevention rule
Circuit breakers and health engines must trip strictly on provider infrastructure failures ($5xx$, $429$, timeouts). Caller-induced authentication or validation errors ($4xx$ excluding 429) must never degrade provider health metrics or trigger failovers.

### Reusable lesson
Applies to all API gateways, service meshes, and reverse proxies. Caller error rates must be decoupled from upstream link availability metrics.

### Related code
- `pkg/circuitbreaker/circuit_breaker.go`: `RecordFailure(statusCode int)`.
- `pkg/gateway/gateway.go`: `handleChatCompletions`.

### Related tests
- `pkg/circuitbreaker/circuit_breaker_test.go`: `TestCircuitBreaker429ImmediateTrip`.
- `pkg/spark/spark_test.go`: `TestSparkEngine429Degradation`.

### Related lessons
- [concurrency-and-lifecycle.md](concurrency-and-lifecycle.md#go-subscription-notification-channel-double-close-panic)

### Status
**Resolved**.

---

## Transparent Multi-Tier Failover with Zero Client-Facing Outage

### Context
In high-throughput AI inference, upstream providers experience transient rate limiting (`429`) or server errors (`502/503/504`) during peak load. Exposing these errors directly to client applications degrades reliability and user experience.

### What happened
Under single-route dispatch, when an upstream provider experiences a transient blip, the gateway returned an immediate `HTTP 502 Bad Gateway` to the caller, despite multiple healthy, equivalent alternative models being available in the cluster.

### Observable symptom
Client applications receive intermittent `502` or `503` errors during upstream provider maintenance or sudden rate limiting.

### Impact
End-user disruption, application retry storms, and SLA violations.

### Incorrect assumption
Assumed that routing begins and ends with selecting a single optimal model path, leaving retry orchestration to the client SDK.

### Root cause
**Confirmed**. Decoupling routing selection from failover execution. A routing decision must produce an ordered list of ranked alternatives ($R_0, R_1, R_2, R_3$) rather than a single static target.

### Why the architecture allowed it
The gateway previously had no coordinated execution loop that combined circuit breaker state checks with route plan fallback execution.

### Fix
Implemented `FailoverCoordinator` in `pkg/circuitbreaker/circuit_breaker.go`. The Decision Engine returns a `RouteDecision` containing `Primary` ($R_0$) and up to 3 `Fallbacks` ($R_1, R_2, R_3$). The coordinator executes the dispatch loop:

1. Checks if breaker for route candidate is allowed (`StateClosed` or `StateHalfOpen`).
2. Dispatches request to the candidate.
3. If candidate succeeds, records success and returns response immediately.
4. If candidate returns 429, 5xx, or network error, records failure in breaker and transparently attempts the next ranked candidate.
5. Injects telemetry header `X-LiteRouter-Attempts: <count>` to inform caller of failover history without exposing failure.

### Verification
Created `TestFailoverCoordinatorTransparentFallback` in `pkg/circuitbreaker/circuit_breaker_test.go`. Simulated primary upstream returning HTTP 500 followed by fallback returning HTTP 200. Verified that the caller receives HTTP 200 with fallback payload and attempts count = 2.

### Prevention rule
Every routing computation must produce an ordered fallback chain. Gateway dispatchers must execute transparent failover across healthy alternatives before returning 502/503 errors to the client.

### Reusable lesson
Applies to payment processors, AI model gateways, and multi-cloud RPC proxies. Single-target dispatch without transparent failover creates unnecessary downtime.

### Related code
- `pkg/circuitbreaker/circuit_breaker.go`: `FailoverCoordinator`, `Execute()`.
- `crates/literouter-decision/src/pareto.rs`: Primary and fallback candidate generation.

### Related tests
- `pkg/circuitbreaker/circuit_breaker_test.go`: `TestFailoverCoordinatorTransparentFallback`.

### Related lessons
- [rust-ffi-and-compilation.md](rust-ffi-and-compilation.md#rust-2024-edition-unsafe-attribute-enforcement-on-c-ffi-exports)

### Status
**Resolved**.
