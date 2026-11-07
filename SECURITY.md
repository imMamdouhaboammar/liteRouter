# Security Policy

## Supported Versions

| Version | Supported |
| :--- | :--- |
| `0.1.x` (main) | Yes |

---

## Reporting a Vulnerability

Do not open public GitHub issues for security vulnerabilities or credential leaks.

### Reporting Channels

- **Email:** Report security concerns directly to `mamdouhfces1997@gmail.com`.
- **GitHub Private Advisory:** Submit an advisory via the repository's "Security" tab -> "Report a vulnerability".

### Information to Include

- Description of the vulnerability and attack vector.
- Minimal reproducible proof-of-concept (PoC) or script.
- Potential impact on cluster state, routing decisions, or secret confidentiality.

### Response Timeline

- **Initial Response:** Within 24 hours.
- **Triage & Assessment:** Within 48 hours.
- **Remediation Release:** Prioritized based on severity (CVSS score).

---

## Security Invariants

1. **Credential Isolation:** The gateway passes authorization headers to upstreams but never persists API keys to disk, link-state gossip tuples, or unmasked logs.
2. **Denial-of-Service Defense:** Upstream 429 rate limits trip circuit breakers with exponential backoff (up to 60s) to prevent thundering-herd retry storms against upstream endpoints.
3. **Memory Safety:** The decision engine critical path is implemented in memory-safe Rust with strictly scoped temporary heaps for Starlark policy evaluations.
