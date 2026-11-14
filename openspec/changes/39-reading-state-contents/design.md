## Context

Tool-neutral masked reads over the separate stack and workspace models, with single-path reveal. Main serves Pulumi masked reads and reveal on the Pulumi routes; this change keeps them and adds one paged tool-neutral read shape with the same masking.

## Decisions

1. **Keep tool routes, add paged tool-neutral masked read plus single-path reveal.** Keep the Pulumi state routes unchanged. Add the NEW tool-neutral masked read as a paged list keyed by value path, plus single-path reveal. Reveal takes unit reference, version and exactly one value path and writes one reveal entry before the first byte. Statuses: 403 missing grant, 404 unknown version, 400 bad, non-secret or cross-version path, 422 Pulumi other provider, 410 shredded or redacted.
2. **Chunked open with bound cursor, strict reveal allowlist and defined failures.** The server parses state in 256 KB chunks and holds at most one 5 MB version. Pages hold 200 markers with an opaque cursor that binds unit reference, version, caller subject and offset, carries an HMAC and expires after 10 minutes. A version above 5 MB is refused with 413. An open or decrypt failure after the audit write answers 500 with no bytes; a timeout at 30 seconds answers 504 with no bytes. A retry writes a second entry.

## Structure

Unexported maskedReader type per tool package with constructor newMaskedReader that binds scope and key; its methods emit markers or open one path.

## Risks

- secret value leaks in masked read, logs or errors
- reveal without audit entry

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| authorize at point of use | handler checks unit:read or unit:read-secrets for the unit | masked read with no grant gives 403; reveal with only unit:read gives 403 |
| boundary check | allowlist for version, value path and cursor; strict JSON decode | bad version and bad JSON Pointer give 400 with no secret echo |
| encode or contain | markers replace values; error text repeats no secret bytes | read state with secrets and check markers plus value path list |
| limit | 200 markers per page, one-path reveal, 30 second timeout | large state gives bounded pages; two paths give 400 |
| fail closed | deny on audit write failure or open failure | break audit write and check reveal answers 500 with no content |

## Diagrams

After this change:

```mermaid
sequenceDiagram
participant C as Client
participant H as NEW tool-neutral state handler
participant Z as Authorizer
participant S as Tool service
participant A as Audit log
C->>H: GET masked state or POST reveal
H->>Z: check unit:read or unit:read-secrets
Z-->>H: allow
H->>S: open masked state or one path
S-->>H: markers or plain value
H->>A: write reveal entry when plain text leaves
H-->>C: markers or one value
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| latency | p95 under 800 | ms | masked state read per unit version under 5 MB |
| storage growth | 1 entry per reveal, kept per audit retention | entries | reveal audit entries per namespace |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart TB
subgraph client[untrusted client]
C[user or workload]
end
subgraph server[server boundary]
H[state handler]
S[tool service]
K[keys]
A[audit log]
end
C-->H-->S-->K
H-->A
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| server boundary | I | secret value leaks in masked read, logs or errors | boundary check | handler allowlists version and value path, strict JSON decode | traversal path and oversized body give 400 with no secret echo |
| server boundary | I | secret value leaks in masked read, logs or errors | encode or contain | markers replace values, errors repeat no secret bytes | read secret state and check no plain bytes in body or logs |
| server boundary | I | secret value leaks in masked read, logs or errors | limit | page size and one-path reveal limit with timeout | full dump request gives bounded pages |
| server boundary | I | secret value leaks in masked read, logs or errors | authorize at point of use | check unit:read or unit:read-secrets on the unit | sibling-unit grant gives 403 |
| server boundary | I | secret value leaks in masked read, logs or errors | fail closed | deny on audit or open failure | break audit store and check 500 with no content |

## Contracts

**stack checkpoint read**

- Invariant: masked read never returns plain secret values
- Invariant: reveal returns one value path with one reveal entry
- `ReadMasked`: pre: caller holds unit:read on the unit. Post: secret values replaced by markers with value paths for the pinned version.
- `Reveal`: pre: caller holds unit:read-secrets on the unit and names one value path. Post: one plain value returned and one reveal entry written before first byte.

## Change impact (computed from main)

Tests:

- `internal/api/runnerapi/plan_test.go` names `secret.ErrShredded`
- `pkg/secret/secrettest/memsealer_test.go` names `secret.ErrShredded`
- `pkg/secret/secrettest/itemkeys_test.go` names `secret.ErrShredded`

ADRs:

- `docs/adr/0154-history-gaps-found-before-the-implementation.md` names `secret.ErrShredded`
- `docs/adr/0102-job-plans-and-logs.md` names `secret.ErrShredded`
- `docs/adr/0152-history-policy-for-job-artifacts.md` names `secret.ErrShredded`
