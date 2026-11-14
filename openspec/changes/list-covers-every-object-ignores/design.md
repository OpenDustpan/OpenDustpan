## Context

A scope with All set cannot name refused namespaces, so lists show namespaces that Authorize refuses.

## Decisions

1. **Exclusion list on the scope.** The scope gets an unexported excluded-namespaces field. Only a constructor makes it; the constructor drops the list when All is false and sorts and dedupes names. The gate fills it per request.

## Structure

Unexported field on the scope value, set only by its constructor.

## Risks

- A consumer that ignores the field fails open; the conformance check covers each consumer.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | NOT IN clause in the namespace filter | List with a refused namespace on sqlite and postgres |
| service | The gate fills exclusions and fails closed on a read error | Gate with a failing NamespacesWithList |
| HTTP handler | Caller access shows the exclusions | Caller access from a refused address |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Gate fill | NamespacesWithList fails | Retry the request | The list call fails closed |

## Change impact (computed from main)

Tests:

- `e2e/claim_gate_test.go` names `identity.Scope`
- `internal/removalalertsvc/removalalertsvc_test.go` names `identity.Scope`
- `e2e/jobs_api_test.go` names `identity.Scope`

Docs pages:

- `docs/guides/access-review.md:200` names `identity.Scope`

ADRs:

- `docs/adr/INDEX.md` names `identity.Scope`
- `docs/adr/0210-the-tool-neutral-api-prefix-paths-actions-versions-and-style.md` names `identity.Scope`
- `docs/adr/0058-console-stack.md` names `identity.Scope`
