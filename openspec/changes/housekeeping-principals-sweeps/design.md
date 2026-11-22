## Context

Part 1 (`housekeeping-principals-install`) made one built-in principal for each housekeeping purpose, with stored grants. This part makes eight purposes run as their principals. It is security code (ADR 250): it decides what the server does with no person in the loop. Part 3 (`housekeeping-principals`) covers the other three purposes and the refusal of a credential.

## Decisions

1. **Each run authorizes.** Each housekeeping service takes its `identity.Principal` from `principal.Housekeeping(purpose)` of part 1 and calls the authorizer before it acts, as any caller does. A denial changes nothing and writes an audit entry with the outcome `denied`. The audit entry and the run record of each run name the principal.
2. **The principal comes in at the construction.** `pkg/dustpan/dustpan.go` passes the principal of each purpose to its service. A service has no default principal and no fallback to `@server`: a service built with no principal does not compile.
3. **A revoke stops one purpose.** The authorizer decides each run from stored grants, so the revoke of the grant of one principal refuses only its own purpose.

4. **The reconcile checks `job:reconcile` (owner, 2026-11-22).** The reconciler (`pkg/job/reconcile.go`) calls the authorizer with the principal of `job-reconcile`, the action `job:reconcile` (NEW in part 1) and the unit of the job, before each change that it applies: the dispatch, the gate, the end of an orphan, the expiry and the other changes of `reconcileRun.apply`. The check is inside `apply`, so a change cannot skip it. The principal holds no start action and no `job:cancel`, so a revoke of `job:reconcile` stops the reconcile and a leaked credential cannot start a plan, an apply or a destroy. `reconcileAction` stays for the audit entry only: the `Action` of the entry still names the effect, and its `Subject` is the principal. A denial changes nothing and writes an audit entry with the outcome `denied`, the principal and `job:reconcile`.
5. **Each sweep checks its own action on its own object.** `artifact-sweep` checks `server:sweep-artifacts`, `blob-sweep` `server:sweep-blobs`, `checkpoint-sweep` `server:sweep-checkpoints`, `name-lock-prune` `server:prune-name-locks`, each on `@server`. `history-retention` checks `unit:redact` on the unit of each redaction. `saved-view-cleanup` checks `view:delete` on the view. `agent-reaper` checks `namespace:manage-agent-runs` on the namespace of each orphan run and `server:sweep-agent-reveals` on `@server` for each sweep of reveals. The role of each purpose and its grants are in part 1.
6. **A namespace purpose does not read the setting of the reach.** The five roles that act inside namespaces hold a stored grant on each namespace, which part 1 writes at the namespace creation. The authorizer decides them as any grant on a namespace, so they work when `access.installationGrantsReachNamespaces` is `false`. A test of each of the five purposes runs with the setting `false`.

## Structure

- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- A service takes its principal as a required constructor argument, so a sweep with no principal cannot be built.
- No custom analyzer and no new tool (ADR 364).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Each housekeeping service | It authorizes before it acts, and names its principal. | `TestRevokeStopsOnlyItsSweep`: revoke the grant of the artifact sweep; the artifact sweep changes nothing and writes a denial; the other sweeps run as before. `TestEachSweepIsAttributed`: each audit entry of each sweep names its housekeeping principal, never `@server`. |
| Authorizer | No code rule: only stored grants decide. | The case of an unknown subject with no grant row is denied. It is the same case as in the authorizer tests on main. |
| Reconcile of jobs | The check of `job:reconcile` is inside `apply`. | `TestReconcileHoldsNoStartAction`: the principal of `job-reconcile` with only its role asks the authorizer for `unit:run-plan`, `unit:run-apply`, `unit:run-destroy`, `unit:write-back` and `job:cancel` on a unit. Each is denied. A reconcile with the grant of `job:reconcile` revoked changes no job. |
| Namespace reach | The grants are on the namespace. | `TestNamespacePurposesWorkWithoutInstallationReach`: with the setting `false`, the retention, the saved view cleanup, the issuer rotation, the reaper and the reconcile run on a new namespace. |

Each purpose has two denial cases: no grant at all, and a grant of another purpose's role. For each service the mutation test removes its authorize call, and `TestRevokeStopsOnlyItsSweep` must fail. The pull request lists the mutations.

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
