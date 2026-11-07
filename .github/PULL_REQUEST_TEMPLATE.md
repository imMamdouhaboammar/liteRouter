## Summary

<!-- Briefly describe the technical intent of this change and the problem it resolves. -->

## Protected Invariants

- [ ] **Concurrency:** Channels exposed to multiple goroutines are wrapped in `sync.Once`.
- [ ] **Rust 2024 Safety:** Exported C FFI symbols use `#[unsafe(no_mangle)]`.
- [ ] **Starlark Lifecycle:** Script execution is scoped within `Module::with_temp_heap`.
- [ ] **Fault Discrimination:** Upstream client errors (401/403/400) do not trip circuit breakers or degrade health scores.
- [ ] **Failover Chain:** Route decisions maintain ordered fallback alternatives (R0 -> R1 -> R2).
- [ ] **Documentation:** Follows the Direct Payload Invariant with zero preambles or affordance narration.

## Testing & Verification

<!-- Include exact commands and terminal output proving this change. -->

```bash
# Run Go race detector and Rust test suite
make test
```

## Checklist

- [ ] Unit or integration tests added for new functionality or bug fixes.
- [ ] Code formatted with `gofmt -s` and `cargo fmt`.
- [ ] Linter checks pass (`cargo clippy --all-targets -- -D warnings`).
- [ ] If a failure class was resolved, documented in `failure-lessons/`.
