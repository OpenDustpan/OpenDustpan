---
version: 10
---

Issue: #1157.

## Why

**Persona: developer.** A developer starts an OpenTofu apply through the jobs API of a namespace that requires approval. The developer needs the job to wait for a second person, and to run only after that person approves. Today the server refuses the start of an apply, so no stored approval default takes effect on a live job.

The user path this change must make work:

1. **API.** Namespace administrator sets the approval default (`PUT /api/dustpan/v1/namespaces/{namespace}/approval-default`) and reads the docs page.
2. **API.** Developer starts an apply job (`POST /api/dustpan/v1/namespaces/{namespace}/jobs`). The plan phase runs, then the job waits in `awaiting-approval`.
3. **API.** A second person approves (`POST .../jobs/{job_id}/approvals`) with the plan binding, and the apply phase dispatches.
4. **Docs page.** Developer reads `docs/guides/approval-rules.md` for the default, the wait state and the approve command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A default saved after the start adds an approval to a job that waits to dispatch. | The job goes to `awaiting-approval` and the reason names the new default. Read the default and the job. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-default` |
| The approver sends the approval without the plan binding of the stored plan. | 409 with the reason. Read the plan in the job document and send its binding. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

## What the change was and what main already does

The first version of this change said that no code wires `job.Decider`. That premise is false. Each fact below was checked on main (7a6c1125a, 20 Nov 2026) with its file and line.

| Part of the first version | State on main | Proof |
| --- | --- | --- |
| Wire `jobgate.Decider` into the job service | DONE | `pkg/dustpan/jobservice.go:45` builds it with `jobgate.New`, and `jobservice.go:69` passes it as `Decider` to `job.NewService`. The call is in `pkg/job/service_start.go:417`. |
| The default gives the phase that changes a gate | DONE | `pkg/gate/jobgate/jobgate.go:73` (`Decide`). Tests: `TestDecideGatesTheApplyPhase` (`jobgate_test.go:189`), `TestDecideFailsClosed` (`:426`), `TestDecideStoreErrorsRefuse` (`:479`). |
| Apply with no default needs no approval | DONE | `TestDecideWithNoDefaultNeedsNoApproval` (`jobgate_test.go:152`). A plan and a drift check get no gate (`:380`, `:396`). |
| A job with a gate waits in `awaiting-approval` and resumes on the last approval | DONE at the service | `TestStartGateOnFirstPhase` (`pkg/job/service_test.go:963`), `TestApproveRecordsTheVerdict` (`service_approve_test.go:154`), `testReconcileGate` (`jobtest/reconcile.go:245`). |
| The wait and the signatures survive a restart | DONE | The gates and approvals are fields of the stored phase, and the store suites run `jobtest` (`jobtest/approve.go:163`, `jobtest/reconcile.go:245`). An approval wait holds no runner claim, so a lease loss does not touch it. |
| A plan that went stale sends the job back to plan | DONE | `ErrPlanStale` (`pkg/job/record.go:492`), `EventReplan` (`pkg/job/state.go:117`), `docs/guides/approval-rules.md:39`. |
| A principal without the action is refused | DONE at the service | `TestStartApplyAndDestroyNeedTheirActions` (`pkg/job/service_applydestroy_test.go:74`). The route side is owned by `planner-role-2`. |
| A gate at change.propose or state.write stops a start and a claim | DONE as a refusal, not as an evaluation | `jobGates.Enforced` (`pkg/dustpan/jobservice.go:275`), `checkGates` (`pkg/job/service_start.go:370`), `gateRefusal` (`pkg/job/claimer.go:512`). The answer says the server "cannot evaluate it for a job yet" (`pkg/job/service.go:85`). |
| **A start of an apply or a destroy** | **MISSING** | `checkPlanRequest` and `startsOnRunner` refuse every operation but a plan (`pkg/dustpan/jobservice.go:597`, `:622`). Test: `TestJobPlannerRefusesApplyAndDestroyWithTheOldAnswer` (`pkg/dustpan/jobservice_internal_test.go:531`). Docs: `docs/reference/jobs-api.md:8`. So `Decide` is reached with a plan only, and `Decide` gives a plan no gate. No approval default takes effect on a live job. |
| **A recheck of the defaults before the apply phase dispatches** | **MISSING** | The gates of a job are stored at the start. `reconcile.go:372` reads only the stored gates. A default saved after the start changes nothing. |
| **A removed default does not release a wait** | PARTLY | It holds by construction, because no code path removes a stored gate. No test names it. |
| **The record of the decision names the policy** | **MISSING** | `Decision.PolicyVersion` is set (`jobgate.go:143`) and no code reads it: `rg -n 'PolicyVersion' --glob '*.go' pkg internal cmd` finds no reader in `pkg/job`. |
| Docs and e2e | **MISSING** | `e2e/jobs_api_test.go:186` says the API starts plan jobs only. `docs/guides/approval-rules.md:16` says that a job that the CLI of the tool starts waits for no approval. |

The first version also named two parts that main does not have and that this change does not need:

- `internal/jobsvc`, `StartJob`, `ApproveJob` and `DispatchApply` do not exist. The job service is `pkg/job` (`Service.Start`, `Service.Approve`) and the dispatch is `Reconciler` (`pkg/job/reconcile.go`).
- The real Pulumi CLI and the real OpenTofu CLI do not start a job through this API. They use their own APIs. The docs say that such a change waits for no approval (`docs/guides/approval-rules.md:16`).

Proof of the first negative claim: `rg -n -F 'jobgate.New' --glob '!*_test.go' pkg internal cmd` finds `pkg/dustpan/jobservice.go:45`.

## What Changes

- The jobs API accepts the operations `apply` and `destroy` for an OpenTofu workspace on a runner. The job runs its plan phase, waits for the approvals that the default names, and dispatches the apply phase after the last approval. A start that the server cannot run (another tool) keeps its refusal.
- Before the reconcile dispatches a queued job, it reads the approval defaults again. A default that adds a requirement puts the job back in `awaiting-approval`. A removed default releases nothing.
- The audit entry of the start names the policy version of the decision.
- E2E drives a real OpenTofu CLI on a runner through plan, wait, approval and apply, with the docs commands.
- `docs/guides/approval-rules.md` and `docs/reference/jobs-api.md` describe the live path.

Out of scope:

- Evaluation of gates by the Cedar engine at change.propose, change.planned and change.apply, with deny, engine errors, facts, evaluate mode and the decision record of gate versions. Owner: `evaluate-gates-on-pulumi-jobs`. Until it lands, a start and a claim that an enforced gate covers stay refused.
- More than one approval requirement, users and unit owners as approvers, and fresh authentication per approval. Owner: `several-approval-requirements-one-change`.
- The 403 of a principal with only plan rights on the HTTP route. Owner: `planner-role-2`.
- A Pulumi stack job on a runner. The docs say that no runner runs the Pulumi plan step yet. This change does not add it.
- Console run page, state.write, profile.save and profile.open, cancel and timeout during a wait.

Depends on:

- `add-job-service-routes-start-freeze-manage`
- `several-approval-requirements-one-change` only for the second requirement. One requirement works without it.

## Usability

- One wait word (`awaiting-approval`) and one approve route.
- The wait names the default that causes it, and the person sees the plan binding to send in the job document.
- An apply that waits after a default changed surprises the developer. The reason of the job names the new default.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 2, 4, 8).

- Decisions that apply: 2 (current), 4 (current), 8 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Six decision points (owner). `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224 decision 2)."
- Quote: "Fixed controls stay fixed (owner). Workspace locks, manual locks, freezes with break-glass, override switches, module switches,"
- Quote: "Words. "Gate" names the stored rule. The job state that waits for people is the **approval wait**, and its wire value stays `awaiting-approval`"
- Effect: The change uses the fixed decision points, the approval wait name, and the plan staleness fixed control.
- Conflict: none.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 2, 13, 14).

- Decisions that apply: 2 (extended), 3 (current), 7 (current), 13 (current), 14 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "Six decision points (owner).  | Decision point | When | Replaces | | --- | --- | --- | | `change.propose` | before a job,"
- Quote: "The recheck at `change.apply`. `change.apply` evaluates the gates again with the facts of that moment. A recheck only adds."
- Quote: "Modes. A gate in `enforce` mode gives its outcome. A gate in `evaluate` mode is evaluated and written in the record, with no effect."
- Quote: "An error fails closed (owner). A diagnostic error of the engine for a gate in `enforce` mode stops the change with `gate.ErrRuleFailed`."
- Quote: "Combining (owner). For each unit, in this order: 1. Any `deny` gives `deny`, with the message of each `deny`. 2. Else each named approval rule"
- Effect: The change wires evaluation at change.propose, change.planned and change.apply, keeps combine, recheck-only-adds, evaluate mode and fail-closed error rules.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (extended).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A fact (owner). A fact is a typed value that the server produces before a decision point. A fact never decides."
- Quote: "A missing fact is a typed value (owner). Each fact always has a value of its type. A fact that a producer can fail to give belongs"
- Effect: The change uses stored approval defaults with server-produced facts and fails closed when a needed fact is unavailable.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Is `job.Decider` wired? Yes. `rg -n -F 'jobgate.New' --glob '!*_test.go' pkg internal cmd` finds `pkg/dustpan/jobservice.go:45`. The first version said it is not.
- Where does the recheck run? In `Reconciler`, which is the one place that dispatches a queued phase (`pkg/job/reconcile.go:372`). It already moves a queued job with a closed gate to `awaiting-approval`. ADR 224 decision 2 gives the recheck at `change.apply`, and "A recheck only adds" gives the removal rule.
- Is a refusal of an enforced Cedar gate still right while this change lands? Yes. The start and the claim refuse a gate that nobody evaluates (`pkg/job/service_start.go:370`). This change does not change that.
- Does the lift of the refusal need a security decision? No. The apply start keeps each existing check: the actions of the starter on the unit, the access limits, the gates guard, the freeze and the fence. The change adds no new way to start a job.

Rewritten on 20 Nov 2026 from the evidence above, replacing the factory draft of the first version. Every path and line was checked on main.
