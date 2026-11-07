# Contributing to liteRouter

Follow these engineering standards, workflows, and testing requirements when contributing to liteRouter.

---

## Development Setup

### Prerequisites

- Go 1.22+
- Rust 1.80+ with Cargo
- Make

### Build and Test

```bash
# Clone the repository
git clone https://github.com/imMamdouhaboammar/liteRouter.git
cd liteRouter

# Build Go gateway and Rust decision library
make build

# Run all unit and integration tests with race detection
make test
```

---

## Engineering Invariants

All contributions must preserve the project's non-negotiable architectural invariants:

1. **Idempotent Channel Teardown:** Never close Go channels directly with `close(ch)` if exposed to multiple goroutines. Wrap channels in a struct with `sync.Once`.
2. **Rust 2024 Safety Attributes:** Exported C FFI symbols must declare `#[unsafe(no_mangle)]`.
3. **Starlark Scoped Heaps:** Embedded Starlark evaluations must execute inside `Module::with_temp_heap(|module| { ... })`.
4. **Error Discrimination:** Client validation errors (400) and auth errors (401/403) must never trip circuit breakers or degrade upstream health metrics. Only 5xx, 429, and network timeouts affect health.
5. **Direct Payload Documentation:** Documentation must transition directly from headers to technical content without conversational preambles or affordance narration.

For full architectural postmortems, see [`failure-lessons/`](failure-lessons/README.md).

---

## Pull Request Guidelines

1. **Branch Naming:** Use `feat/<feature-name>`, `fix/<bug-name>`, or `refactor/<target>`.
2. **Atomic Commits:** Follow Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
3. **Regression Tests:** Every bug fix must include an automated regression test in the relevant package.
4. **Race Detection:** All Go tests must pass under `go test -race`.
5. **Verification Proof:** Include test execution output in PR descriptions.

---

## Code Style

- **Go:** Format with `gofmt -s`. Adhere to standard library conventions. Keep cyclomatic complexity low.
- **Rust:** Format with `cargo fmt --check`. Ensure clean builds with `cargo clippy --all-targets -- -D warnings`.
