# Concurrency and Lifecycle Failure Lessons

This document details failure classes and engineering invariants related to Go concurrency, goroutine orchestration, and resource lifecycle management.

---

## Go Subscription Notification Channel Double-Close Panic

### Context
In `pkg/kvstore/kvstore.go`, client goroutines subscribe to key-value mutation deltas via `Subscribe(prefix string) (<-chan DeltaEvent, func())`. Subscribers receive a cancel/unsubscribe function, while the store also maintains a background `Close()` method to clean up all active subscribers on server shutdown.

### What happened
During unit testing of subscriber teardown under concurrent load, an unsubscribe callback was invoked concurrently with `kv.Close()`. Both code paths executed `close(ch)` on the same underlying channel, triggering a fatal runtime panic.

### Observable symptom
```
panic: close of closed channel
goroutine 42 [running]:
github.com/imMamdouhaboammar/liteRouter/pkg/kvstore.(*KvStore).Close(...)
    /pkg/kvstore/kvstore.go:215
```

### Impact
Server crashes immediately. In a production cluster running thousands of concurrent subscriber goroutines (such as SSE streams or Spark health listeners), any simultaneous client disconnect and cluster node rebalancing would crash the entire node.

### Incorrect assumption
Assumed that guarding map mutations with a read-write mutex (`sync.RWMutex`) was sufficient to prevent double closing of channels held in the map. However, once a channel reference is acquired by a caller, releasing the lock allows another goroutine to invoke `close(ch)` on the identical channel reference.

### Root cause
**Confirmed**. In Go runtime semantics, closing an already closed channel causes an unrecoverable panic. Relying on map presence as a proxy for channel open state creates a Time-of-Check to Time-of-Use (TOCTOU) race condition when unsubscription and shutdown race.

### Why the architecture allowed it
The channel was stored directly as a naked type (`map[chan DeltaEvent]string`) without encapsulating lifecycle state or idempotency guarantees inside a dedicated object.

### Fix
Introduced an internal `subscription` struct wrapping the event channel alongside a `sync.Once` primitive:

```go
type subscription struct {
    ch     chan DeltaEvent
    prefix string
    once   sync.Once
}

func (s *subscription) close() {
    s.once.Do(func() {
        close(s.ch)
    })
}
```

The KvStore tracks subscribers by pointer (`map[*subscription]struct{}`). Both `unsubscribe()` and `kv.Close()` delegate to `s.close()`, guaranteeing that `close(ch)` executes strictly once regardless of invocation order or concurrency.

### Verification
Added `TestKvStoreSubscribeDelta` in `pkg/kvstore/kvstore_test.go` exercising concurrent puts, event delivery, and racing unsubscribe with `kv.Close()`. Executed with `go test -v -count=1 -race ./pkg/kvstore`. Test passed with zero race conditions and zero panics.

### Prevention rule
Any Go channel that can be terminated from multiple call sites (e.g., client cancellation and server shutdown) must be encapsulated inside a struct with a `sync.Once` close guard. Never expose raw channels for multi-party closure.

### Reusable lesson
Applies to all pub/sub engines, event buses, WebSocket connection managers, and SSE broadcasters. Whenever an entity can be closed by both a timeout handler and a client termination handler, `sync.Once` is mandatory.

### Related code
- `pkg/kvstore/kvstore.go`: `subscription` struct and `close()` method.
- `pkg/kvstore/kvstore.go`: `Subscribe()` and `Close()` implementations.

### Related tests
- `pkg/kvstore/kvstore_test.go`: `TestKvStoreSubscribeDelta`.

### Related lessons
- [gateway-and-fault-isolation.md](gateway-and-fault-isolation.md#transparent-multi-tier-failover-with-zero-client-facing-outage)

### Status
**Resolved**.
