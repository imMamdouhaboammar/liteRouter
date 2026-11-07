# liteRouter Failure Lessons & Project Memory

> **The Core Invariant:**
> *"Pay for an engineering mistake once. After that, the project should remember it."*

This repository maintains an active, durable knowledge base of engineering failures, design revisions, debugging breakthroughs, and non-negotiable invariants discovered during the development of `liteRouter`.

---

## Purpose & Scope

Every lesson recorded here represents a real failure mode encountered during implementation, testing, or review. Rather than burying debugging breakthroughs in ephemeral conversation history or squashed commits, this directory provides:

1. **Systemic Root-Cause Analysis:** Explains why the architecture permitted the failure mode to occur.
2. **Deterministic Prevention Invariants:** Concrete engineering rules that prevent the failure class from re-emerging.
3. **Automated Regression Proofs:** Links each failure class directly to an automated test exercising the invariant under race detection (`-race`).

---

## Directory Organization

```
failure-lessons/
├── README.md                           # Directory principles and update triggers
├── lessons-index.md                    # Compact registry table and "Rules We Now Enforce"
├── testing-and-verification.md         # Failure-to-test mapping and falsification proofs
├── concurrency-and-lifecycle.md        # Go channel lifecycle, concurrency, and race conditions
├── rust-ffi-and-compilation.md         # Rust 2024 unsafe attributes and Starlark engine bindings
├── gateway-and-fault-isolation.md      # Circuit breaker error classification and failover
└── documentation-and-slop-prevention.md # Direct payload invariant and anti-slop documentation
```

---

## Update Triggers

Update this directory when:
- A new failure class, race condition, or panic occurs during local testing or production.
- A library upgrade (Go, Rust crates) introduces breaking API changes.
- A regression test fails or reveals an unhandled edge case.
- An existing invariant is strengthened, superseded, or consolidated.

---

## Canonical Failure Lesson Schema

Every lesson within topic files adheres to the 15-field OmniSkill standard:
1. `Context`
2. `What happened`
3. `Observable symptom`
4. `Impact`
5. `Incorrect assumption`
6. `Root cause` (Confirmed | Strongly indicated | Open hypothesis | Unknown)
7. `Why the architecture allowed it`
8. `Fix`
9. `Verification`
10. `Prevention rule`
11. `Reusable lesson`
12. `Related code`
13. `Related tests`
14. `Related lessons`
15. `Status` (Resolved | Partially mitigated | Unresolved | Superseded)
