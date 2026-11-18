## Context

Part 1 (`housekeeping-principals-install`) made one built-in principal for each housekeeping purpose, with stored grants. This part makes eight purposes run as their principals. It is security code (ADR 250): it decides what the server does with no person in the loop. Part 3 (`housekeeping-principals`) covers the other three purposes and the refusal of a credential.

## Decisions

1. **Each run authorizes.** Each housekeeping service takes its `identity.Principal` from `principal.Housekeeping(purpose)` of part 1 and calls the authorizer before it acts, as any caller does. A denial changes nothing and writes an audit entry with the outcome `denied`. The audit entry and the run record of each run name the principal.
2. **The principal comes in at the construction.** `pkg/dustpan/dustpan.go` passes the principal of each purpose to its service. A service has no default principal and no fallback to `@server`: a service built with no principal does not compile.
3. **A revoke stops one purpose.** The authorizer decides each run from stored grants, so the revoke of the grant of one principal refuses only its own purpose.

## Structure

- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- A service takes its principal as a required constructor argument, so a sweep with no principal cannot be built.
- No custom analyzer and no new tool (ADR 364).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Each housekeeping service | It authorizes before it acts, and names its principal. | `TestRevokeStopsOnlyItsSweep`: revoke the grant of the artifact sweep; the artifact sweep changes nothing and writes a denial; the other sweeps run as before. `TestEachSweepIsAttributed`: each audit entry of each sweep names its housekeeping principal, never `@server`. |
| Authorizer | No code rule: only stored grants decide. | The case of an unknown subject with no grant row is denied. It is the same case as in the authorizer tests on main. |

For each service the mutation test removes its authorize call, and `TestRevokeStopsOnlyItsSweep` must fail. The pull request lists the mutations.

## Callers on main (task 0.1, checked on 17 November 2026)

| Purpose | Caller on main |
| --- | --- |
| `artifact-sweep` | `pkg/job/artifactsweep.go:654` |
| `job-reconcile` | `pkg/job/reconcile.go:673` |
| `saved-view-cleanup` | `pkg/savedview/service.go:259` and `:293` |
| `history-retention` | `pkg/history/redaction.go:22` (actor `@server`, reason `RetentionReason`) |
| `name-lock-prune` | `internal/localusersvc/locks.go:415` |
| `agent-reaper` | `internal/agent/reaper.go:53` |
| `checkpoint-sweep` | `pkg/stack/superseded_sweep.go:72` |
| `blob-sweep` | `pkg/workspace/service.go:2527` |

Not in scope (read only here): the notifications that name `@server` as the sender, the drop of runner credentials at a restore, and the sites of the namespace service principal. The design document of part 1 gives the rule for a purpose.

## Risks

- A sweep that a person revoked stops until a person grants it again. The docs page of part 3 says so, and the denial entry names the principal and the action.
- `pkg/dustpan/dustpan.go` is a shared file. Another change that edits the construction part of it may conflict on a line. The resolution is a mechanical one.
