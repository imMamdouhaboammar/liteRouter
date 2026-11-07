# Documentation and Slop Prevention Failure Lessons

This document details failure classes and engineering invariants related to developer documentation, anti-slop engineering standards, and technical accuracy.

---

## Promotional Fluff and Preamble Slop in Technical Documentation

### Context
When writing project READMEs, architecture specifications, and API guides, AI coding assistants routinely default to promotional marketing copy, conversational signposting, and affordance narration.

### What happened
The initial draft of the `liteRouter` README contained conversational preambles (`"The following table outlines..."`, `"In this section, we explore..."`), emoji headers (`# liteRouter ⚡`), superlative buzzword inflation (`"ultra-low-latency"`, `"production-grade"`, `"blazing-fast"`), and patronizing affordance narration.

### Observable symptom
Readers must wade through paragraphs of non-actionable fluff, promotional claims, and conversational transitions before reaching actionable commands, schemas, or code snippets.

### Impact
1. Degrades engineering credibility and developer trust.
2. Increases reading time and obscures key failure modes, configurations, and API signatures.
3. Violates repository constitution and anti-slop quality gates.

### Incorrect assumption
Assumed that technical documentation should read like a marketing product landing page with introductory conversational transitions between sections.

### Root cause
**Confirmed**. Default LLM generation templates prioritize polite conversational padding, superlative adjectives, and affordance narration unless strictly governed by deterministic constraints.

### Why the architecture allowed it
Absence of automated linting or explicit enforcement of the Direct Payload Invariant during initial drafting.

### Fix
Conducted a full rewrite of `README.md` enforcing:
1. **The Direct Payload Invariant:** Transition directly from domain headings to technical payloads (tables, code snippets, ASCII architecture diagrams, math formulas, JSON schemas) with zero introductory fluff.
2. **Elimination of Affordance Narration:** Cut all instructions explaining standard browser/terminal affordances (`"click to expand"`, `"see below"`, `"copy this command to your terminal"`).
3. **Strict Banned Vocabulary:** Zero tolerance for buzzwords (`"seamlessly"`, `"robust"`, `"cutting-edge"`, `"game-changer"`, `"effortlessly"`, `"delve"`, `"leverage"`, `"utilize"`).
4. **Verifiable Code Snippets:** Every command and curl example was executed against the local build to verify verbatim functionality and realistic telemetry headers.

### Verification
Audited `README.md` with:
```bash
grep -iE 'seamlessly|robust|cutting-edge|game-changer|delve|leverage|utilize|ultra-low-latency|blazing-fast' README.md
```
Returned zero matches. Re-verified all code snippets by executing `make build`, `make test`, and local curl requests.

### Prevention rule
All technical documentation must adhere to the Direct Payload Invariant: transition directly from domain headers into technical payloads with zero conversational preambles, zero affordance narration, and zero promotional buzzwords. All code examples must be proven to run.

### Reusable lesson
Applies to all README files, API documentation, architecture decision records (ADRs), pull request descriptions, and release notes across all projects.

### Related code
- `README.md`: Production developer documentation.
- `rules/default_routing.star`: Verifiable policy example.

### Related tests
- `make test` execution output embedded in `README.md`.
- End-to-end curl response headers verification.

### Related lessons
- [gateway-and-fault-isolation.md](gateway-and-fault-isolation.md#upstream-error-classification-and-circuit-breaker-discrimination)

### Status
**Resolved**.
