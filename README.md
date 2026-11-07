# liteRouter

> Multi-provider AI model router using link-state gossip and Pareto shortest-path-first selection.

Inspired by the routing architecture of [Meta OpenR](https://github.com/meta/openr), liteRouter manages traffic across heterogeneous AI providers (OpenAI, DeepSeek, Anthropic, local vLLM). It decouples state synchronization, background health probing, and mathematical route optimization into independent subsystems.

---

## Quick Start

### Prerequisites

- Go 1.22+
- Rust 1.80+ with Cargo

### Build and Run

```bash
make build
make run
```

### Send a Request

```bash
curl -i -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Tenant-Tier: standard" \
  -d '{
    "model": "auto",
    "messages": [
      {"role": "user", "content": "Write a binary search function in Go"}
    ]
  }'
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-LiteRouter-Provider: deepseek-api
X-LiteRouter-Model: deepseek-v3
X-LiteRouter-Attempts: 1
X-LiteRouter-Latency-Us: 48
```

---

## Architecture

```
+-----------------------------------------------------------------------------------+
|                                     liteRouter                                    |
|                                                                                   |
|   +----------------------------------+     +----------------------------------+   |
|   |         AI Spark Engine          |     |        Distributed KvStore       |   |
|   |  - HTTP /models micro-probes     | --> |  - Versioned key tuples          |   |
|   |  - Exponential moving avg (EMA)  |     |  - Total-order conflict resolver |   |
|   |  - 429 & 5xx error tracking      |     |  - Background TTL expiration     |   |
|   +----------------------------------+     +----------------------------------+   |
|                     |                                       |                     |
|                     v                                       v                     |
|   +---------------------------------------------------------------------------+   |
|   |                     Decision Capability Engine (Rust)                     |   |
|   |  - Intent classifier (Code, Math, Reasoning, Creative, General)           |   |
|   |  - Hard constraint pruning (Context window, health threshold, price caps) |   |
|   |  - Multi-objective Pareto SPF scoring                                     |   |
|   |  - Starlark policy evaluation                                             |   |
|   +---------------------------------------------------------------------------+   |
|                                     |                                             |
|                                     v                                             |
|   +---------------------------------------------------------------------------+   |
|   |                     Failover & Circuit Breaker                            |   |
|   |  - Closed -> Open (3 consecutive failures or 429) -> Half-Open            |   |
|   |  - Ranked fallback execution (R0 -> R1 -> R2)                             |   |
|   +---------------------------------------------------------------------------+   |
|                                     |                                             |
|                                     v                                             |
|          OpenAI Direct   |   DeepSeek API   |   Anthropic   |   Local vLLM        |
+-----------------------------------------------------------------------------------+
```

### OpenR Systems Mapping

| OpenR Primitive | liteRouter Equivalent | Implementation |
| :--- | :--- | :--- |
| **Router Node** | Upstream Provider | Physical or virtual API target (`openai-direct`, `deepseek-api`) |
| **Link Adjacency** | Model Channel | Active upstream route (`deepseek-v3`, `gpt-4o`, `claude-3-5-sonnet`) |
| **Spark Tracker** | Spark Health Engine | Background prober tracking latency EMA and status codes |
| **Link Metric Cost** | Composite Cost | Weighted calculation over token price, TTFT EMA, and failure penalties |
| **KvStore Flooding** | Link-State KvStore | In-memory key-value store with versioning and delta pub/sub |
| **Decision Engine (SPF)** | Pareto Router | Sub-millisecond constraint filter and multi-objective scoring in Rust |
| **FIB Hardware** | L7 API Gateway | HTTP reverse proxy serving OpenAI-compatible endpoints |
| **Flap Damping** | Circuit Breaker | Exponential backoff on rate limits (`429`) with automatic failover |

---

## Routing Pipeline

```
Incoming Request
      │
      ▼
1. Intent Profiling ────► Identifies workload (Code, Math, Creative, Reasoning)
      │                   Extracts token count and tenant tier
      ▼
2. Hard Filter     ────► Eliminates dead models (health <= 0.05)
      │                   Eliminates models with context window < prompt tokens
      │                   Eliminates models violating cost or latency ceilings
      ▼
3. Pareto Scoring  ────► Calculates fitness score per eligible model
      │                   Sorts models into primary (R0) and fallbacks (R1..R3)
      ▼
4. Failover Exec   ────► Dispatches to R0
                          Transparently falls back to R1 on 429/5xx/timeout
```

### Fitness Formula

$$F = \left( w_q \cdot Q - w_c \cdot C_{\text{norm}} - w_l \cdot L_{\text{norm}} \right) \cdot H$$

- **Quality Score ($Q$):** Normalized score in $[0.0, 1.0]$ based on intent domain weights:
  - Code: $(0.7 \cdot \text{Code} + 0.3 \cdot \text{Reasoning}) / 5.0$
  - Math / Reasoning: $(0.6 \cdot \text{Reasoning} + 0.4 \cdot \text{Math}) / 5.0$
  - Creative: $(0.8 \cdot \text{Creative} + 0.2 \cdot \text{Reasoning}) / 5.0$
  - General: $(\text{Reasoning} + \text{Code} + \text{Creative}) / 15.0$
- **Normalized Cost ($C_{\text{norm}}$):** $\max(0, \ln(P_{\text{prompt}} + 0.1)) / 4.0$
- **Normalized Latency ($L_{\text{norm}}$):** $\text{clamp}(\text{TTFT}_{\text{ema}} / 1000, 0.05, 3.0) / 3.0$
- **Health Factor ($H$):** Active health score in $[0.0, 1.0]$ reported by Spark.

### Tier Weights

| Tenant Tier (`X-Tenant-Tier`) | Quality Weight ($w_q$) | Cost Weight ($w_c$) | Latency Weight ($w_l$) |
| :--- | :--- | :--- | :--- |
| `enterprise` | 0.60 | 0.15 | 0.25 |
| `standard` (default) | 0.50 | 0.30 | 0.20 |
| `free` | 0.30 | 0.55 | 0.15 |

---

## Dynamic Routing Policies

Custom routing policies are written in Starlark and loaded at startup without recompiling the binary.

```python
# rules/default_routing.star

def route_policy(ctx, candidates):
    # Route VIP enterprise tier requests to Claude 3.5 Sonnet
    if ctx.get("tenant_tier") == "vip":
        return "claude-3-5-sonnet"

    # Route free tier requests to DeepSeek-V3 for cost optimization
    if ctx.get("tenant_tier") == "free":
        return "deepseek-v3"

    # Return empty string to defer to the Pareto decision engine
    return ""

ROUTE_OVERRIDE = ""
```

---

## Configuration Reference

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--port` | int | `8080` | Port for the HTTP API gateway |
| `--node-id` | string | `literouter-edge-01` | Cluster node identifier for KvStore origin tracking |
| `--policy` | string | `rules/default_routing.star` | Path to the Starlark routing policy file |

---

## Provider and Model Registration

Providers and models register directly into the `KvStore` model registry:

```go
// Register an upstream provider
kv.Registry().RegisterProvider(kvstore.Provider{
    ID:             "deepseek-api",
    Name:           "DeepSeek Cloud API",
    BaseURL:        "https://api.deepseek.com/v1",
    APIKeyEnv:      "DEEPSEEK_API_KEY",
    Status:         kvstore.StatusHealthy,
    MaxConcurrency: 800,
})

// Register a model with capability scores (0.0 to 5.0) and pricing
kv.Registry().RegisterModel(kvstore.Model{
    ID:         "deepseek-v3",
    ProviderID: "deepseek-api",
    Capabilities: kvstore.CapabilityScores{
        Reasoning:          4.7,
        Code:               4.9,
        Math:               4.8,
        Creative:           4.2,
        ContextWindow:      64000,
        LatencySensitivity: 4.0,
    },
    Pricing: kvstore.Pricing{
        PromptPerMillionTokens:     0.14,
        CompletionPerMillionTokens: 0.28,
    },
    HealthScore: 1.0,
    Active:      true,
})
```

---

## Circuit Breaker and Failover

Each upstream provider is governed by an independent state machine:

```
           ┌────────────────────────┐
           │         Closed         │◄───────────────────────┐
           │    (Normal traffic)    │                        │
           └───────────┬────────────┘                        │
                       │ 3 failures or 429                   │ 2 successes
                       ▼                                     │
           ┌────────────────────────┐                        │
           │          Open          │                        │
           │   (Traffic blocked)    │                        │
           └───────────┬────────────┘                        │
                       │ Cooldown expires                    │
                       ▼                                     │
           ┌────────────────────────┐                        │
           │       Half-Open        │────────────────────────┘
           │     (Probe trial)      │
           └────────────────────────┘
```

| Event | Condition | State Transition | Action |
| :--- | :--- | :--- | :--- |
| Upstream `429` | Single HTTP 429 response | `Closed` -> `Open` | Trips immediately; sets cooldown (starts at 5s, doubles up to 60s) |
| Upstream `5xx` / timeout | 3 consecutive failures | `Closed` -> `Open` | Trips breaker; 10s cooldown |
| Cooldown expiration | `time.Since(change) > cooldown` | `Open` -> `Half-Open` | Admits trial requests |
| Probe success | 2 consecutive 2xx responses | `Half-Open` -> `Closed` | Resets failure counters to 0 |

### Failover Execution

When a route fails with a 429, 5xx, or network error, `FailoverCoordinator` attempts fallback routes in rank order (`R1`, `R2`, `R3`). The client receives a successful response with the telemetry header `X-LiteRouter-Attempts: 2` instead of an HTTP 502/503.

---

## API Reference

### `POST /v1/chat/completions`

Dispatches chat completion requests through the decision engine.

#### Request Headers

| Header | Required | Default | Description |
| :--- | :--- | :--- | :--- |
| `Content-Type` | Yes | - | Must be `application/json` |
| `X-Tenant-Tier` | No | `standard` | Routing tier: `enterprise`, `standard`, or `free` |

#### Request Body

```json
{
  "model": "auto",
  "messages": [
    {"role": "user", "content": "Write a binary search function in Go"}
  ],
  "temperature": 0.7,
  "max_tokens": 1024,
  "stream": false
}
```

#### Response Headers

| Header | Description |
| :--- | :--- |
| `X-LiteRouter-Provider` | Upstream provider handling the request (`deepseek-api`, `openai-direct`, `anthropic-direct`) |
| `X-LiteRouter-Model` | Selected model (`deepseek-v3`, `gpt-4o`, `claude-3-5-sonnet`) |
| `X-LiteRouter-Attempts` | Number of upstream dispatch attempts before success |
| `X-LiteRouter-Latency-Us` | Decision engine execution latency in microseconds |

---

### `GET /v1/models`

Returns active models registered in the KvStore.

```bash
curl -s http://localhost:8080/v1/models
```

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-3-5-sonnet",
      "provider_id": "anthropic-direct",
      "capabilities": {
        "reasoning": 4.9,
        "code": 5.0,
        "math": 4.8,
        "creative": 4.9,
        "context_window": 200000,
        "latency_sensitivity": 4.2
      },
      "pricing": {
        "prompt_per_million": 3.0,
        "completion_per_million": 15.0
      },
      "health_score": 1.0,
      "active": true
    }
  ]
}
```

---

### `GET /healthz`

L7 readiness check.

```bash
curl -s http://localhost:8080/healthz
```

```json
{"status":"ok","engine":"liteRouter"}
```

---

## Verification

Run all test suites across Go and Rust with the Go race detector enabled:

```bash
make test
```

Expected output:

```
==> Running Rust unit tests...
cargo test --manifest-path crates/literouter-decision/Cargo.toml
running 3 tests
test tests::test_enterprise_tier_prioritizes_top_quality ... ok
test tests::test_code_routing_deepseek_cost_efficiency ... ok
test tests::test_starlark_override ... ok
test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out

==> Running Go unit and integration tests with race detector...
go test -v -count=1 -race ./...
PASS: pkg/circuitbreaker (3 tests)
PASS: pkg/decision (4 tests)
PASS: pkg/gateway (3 tests)
PASS: pkg/kvstore (5 tests)
PASS: pkg/spark (3 tests)
```

---

## Project Structure

```
liteRouter/
├── Makefile
├── cmd/
│   └── literouter/
│       └── main.go                    # Entrypoint and service wiring
├── crates/
│   └── literouter-decision/           # Rust decision core
│       ├── Cargo.toml
│       └── src/
│           ├── capability.rs          # Data types and intent classifier
│           ├── ffi.rs                 # C-compatible FFI exports
│           ├── lib.rs                 # Library entrypoint and tests
│           ├── pareto.rs              # Multi-objective Pareto SPF router
│           └── starlark_policy.rs     # Starlark engine integration
├── pkg/
│   ├── circuitbreaker/                # Adaptive circuit breaker and failover
│   ├── decision/                      # Go bridge to decision engine
│   ├── gateway/                       # OpenAI-compatible HTTP/SSE proxy
│   ├── kvstore/                       # Link-state key-value store
│   └── spark/                         # Health probing and EMA tracking
└── rules/
    └── default_routing.star           # Starlark dynamic routing policy
```

---

## License

MIT
