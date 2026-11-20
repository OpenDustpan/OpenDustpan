---
version: 7
---

Issue: #1158.

## Why

**Persona: developer.** A developer starts a job on a unit that two approval defaults guard. The namespace default needs 2 approvers of group `platform`. The collection default needs 1 approver of any kind. Today the start of the job fails, so the team must move both defaults into one scope. The developer wants the job to wait for 3 distinct people and then run.

The user path this change must make work:

1. **Docs page.** Read (read only) `docs/guides/approval-rules.md` and set one approval default in the namespace and one in a collection.
2. **API.** `POST /api/dustpan/v1/namespaces/{namespace}/jobs` starts the job. The job waits in `awaiting-approval`. `GET .../jobs/{job_id}` lists each requirement of the phase with its count and how many slots are filled.
3. **API.** Each approver sends `POST .../jobs/{job_id}/approvals` with `{"verdict": "approve"}`. The job runs when the matching fills every slot.

Errors the person may hit, and the one command that helps. Each row exists on main or is added by this change:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The start of a job fails because two defaults apply (exists on main: (read only) `pkg/gate/jobgate/jobgate.go:168`) | After this change, the start succeeds for groups-only requirements. For a default that names users or unit owners, sets `freshAuthSeconds` or holds an `unless` body, the start still fails with the same error, because these shapes wait for the open questions below. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-default` |
| 403 on an approval: the subject is not a member of a group of any requirement of the phase (exists on main in `checkGroups`, (read only) `pkg/job/service_approve.go:253`) | After this change, the answer names the requirements of the phase that the subject cannot fill. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| 409 `job.already-approved` (exists: (read only) `internal/errcode/catalog_job.go:9`) | The caller approved this phase already. One subject fills one slot. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- A job holds one gate per phase kind, with one count. Proof: `Decision.Gates` is `map[PhaseKind]Gate` ((read only) `pkg/job/gate.go:428`) and `PolicyGates` copies one gate for each kind ((read only) `pkg/job/gate.go:129`).
- The decider refuses more than one requirement. Proof: `gateOf` returns an error that wraps `gate.ErrUnavailable` when `len(reqs) > 1` ((read only) `pkg/gate/jobgate/jobgate.go:168`). The test `TestDecideFailsClosed` fixes this refusal ((read only) `pkg/gate/jobgate/jobgate_test.go:426`).
- `Gate.Status` and `GatesStatus` count each gate alone, with no rule that a person fills one slot across gates ((read only) `pkg/job/gate.go:236`, (read only) `pkg/job/gate.go:295`). So a phase with two gates would count one person twice.
- `checkGroups` demands that the approver is in the groups of every gate that names groups ((read only) `pkg/job/service_approve.go:253`). With several gates, one approver could never be a member of two disjoint groups, so a phase with two group gates would never open.
- The approval state is in the JSON body of the job row, so the SQL baselines do not change. Proof: `gateBody` and `approvalBody` in (read only) `pkg/store/internal/jobstore/body.go:127` and `:144` are the one codec of both dialects.
- The jobs API already returns the gates and the approvals of each phase ((read only) `internal/api/jobs/serve.go:92`, `:412`, `:419`).
- The approval defaults give at most 3 requirements for one unit. Proof: `UnitChain` returns the namespace, the collection and the unit scopes ((read only) `pkg/gate/scope.go:459`), and each scope holds one default ((read only) `pkg/gate/rules.go:298`). `DefaultRequirements` turns each default into one requirement or merges it into `default` ((read only) `pkg/gate/requirements.go:30`).

## What Changes

The change has two parts. Part A is buildable now, because ADR 225 decision 6 and ADR 192 decision 6 decide it. Part B waits for the owner (see "Open questions").

**Part A: several group requirements on one job, with distinct people.**

- `pkg/job/gate.go`: `Gate` gets a NEW field `Name`, the name of its requirement (`default`, `default/<scope key>`). `Decision.Gates` (exists, `map[PhaseKind]Gate`) becomes `map[PhaseKind][]Gate` (NEW type), and `PolicyGates` (`:129`) copies each gate of a kind. `Approval` gets a NEW field `For`: the names of the gates whose groups held the approver when the approver signed. `GatesStatus` (`:295`) becomes a maximum matching: each gate has `Approvals` slots, and one subject fills one slot of one gate. A new file `pkg/job/match.go` holds the matching, with augmenting paths over at most 3 gates.
- `pkg/job/service_approve.go`: `checkApprover` (`:208`) and `checkGroups` (`:253`) check each gate alone and set the NEW field `For` of the approval to the gates that hold the approver. An approval whose `For` is empty is refused with `identity.ErrDenied`. `record` (`:347`) and `approvalEntry` (`:391`) write the names of `For` to the audit entry. `waitingGates` (`:174`) stays.
- `pkg/job/reconcile.go:372` and `pkg/job/service_start.go:735`: both call `GatesStatus` and need no code change. The tests prove that they use the matching.
- `pkg/job/service_start.go:439`: the start refuses a decision with more than 3 gates for a phase, with `ErrInvalid` (defense in depth, see the design).
- `pkg/gate/jobgate/jobgate.go`: `gateOf` (`:167`) becomes `gatesOf`. It returns one `job.Gate` for each requirement, with the name set. It keeps the refusal, with the same text, for a requirement that names users or unit owners, sets `freshAuthSeconds`, or has groups saved with no group IDs. `Decide` (`:73`) sets the `BindPlan` field on every gate of a changing phase.
- `pkg/store/internal/jobstore/body.go`: `gateBody` gets `name`, `approvalBody` gets `for`. The stores of both dialects share this codec, and the JSON column `body` of the jobs table holds this state, so the baselines `pkg/store/sqlite/migrations/` and `pkg/store/postgres/migrations/` do not change and no migration is added.
- `internal/api/jobs/serve.go:130-145`: `gateResponse` gets `name` and `filled`, `approvalResponse` gets `for`. The API description and the clients are generated again with `make generate` and `make clients-generate`.
- `docs/guides/approval-rules.md`: the section "One requirement only" (`:31-37`) is replaced. `docs/guides/jobs.md` (approve section, `:77`) shows the new response fields.

**Part B: users, unit owners, fresh authentication and unless on a job (blocked).** Each item needs a decision of the owner. The tasks of Part B are in `tasks.md` and marked blocked. They start only after the open questions have answers.

Out of scope:

- Withdrawal of an approval. No ADR gives a withdrawal verdict, and the route `POST .../approvals` accepts only `approve` and `reject` (`internal/api/jobs/approve.go`). A withdrawal needs an ADR for the verdict, who may withdraw, and what the audit entry says. It is a new capability and gets its own change.
- A cap of 10 requirements and a 429 refusal. The cap has no counting point: the defaults give at most 3 requirements, so the structure bounds the count. The check of 3 in the start is the guard.
- Ownership rules (`owners/<id>` requirements) on jobs. Main does not read ownership rules in `jobgate`, and this change does not add them.
- Named approval rules on jobs (`approval:<ID>` gates). Jobs read approval defaults only.
- Change orders. They have their own matcher (the matcher of change orders, ADR 192 decision 6).
- Gate configuration routes, the simulate route and the console run page.
- A recheck of the group membership at dispatch, until question 3 has an answer.

Depends on: none for Part A.

Part B starts after the changes session-fresh-cookie, fresh-auth-enabled-setting and fix-destroy-approval-fresh-auth are on main: they give the fresh-authentication fact.

Not a dependency: the change approval-defaults-take-effect-live. Its premise (the decider is wired nowhere) is false on main: (read only) `pkg/dustpan/jobservice.go:45` builds `jobgate.New`. Mark that change stale in its own pull request.
- The changes fresh-authentication-window-0-turns and gate-evaluation-must-apply-dev are not dependencies of Part A.

## Usability

- The job read response lists each requirement with its count and its filled slots, so a developer sees what waits before asking anyone.
- A refused approver gets the names of the requirements that they cannot fill, not a general 403.
- The start of a job with groups-only defaults in two scopes works the first time from the docs page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `Gate` is one requirement: one count, one list of groups ((read only) `pkg/job/gate.go:89`).
- A phase holds `[]Gate` (`phase.Gates`, (read only) `pkg/store/internal/jobstore/body.go:164`), but the policy fills one gate for each kind (`PolicyGates`, (read only) `pkg/job/gate.go:129`).
- `Approve` reads the waiting gates, checks the approver and records the verdict ((read only) `pkg/job/service_approve.go:38`, `:174`, `:208`, `:347`).
- `record` picks `EventApproved`, `EventApprovalRecorded` or `EventRejected` from `GatesStatus` ((read only) `pkg/job/service_approve.go:357`).
- The reconciler dispatches a queued job when `GatesStatus` is open ((read only) `pkg/job/reconcile.go:372`). The store checks the plan binding, the lock, the busy unit and the destroy proof in `Store.Apply` for `EventDispatch` ((read only) `pkg/job/store.go:247`, `checkDestroyProof` in (read only) `pkg/store/internal/jobstore/events.go`).
- A rejection by any holder of `job:approve` rejects the job. It needs no group check ((read only) `pkg/job/service_approve.go:251`).
- The route `POST .../jobs/{job_id}/approvals` declares `job:read` and `job:approve` on the unit of the job ((read only) `internal/api/jobs/routes.go:157`).
- `DefaultRequirements` refuses a default with an `unless` body ((read only) `pkg/gate/requirements.go:44`). `Unless` is a field of `ApprovalDefault` ((read only) `pkg/gate/rules.go:310`), not of `ApprovalRule` (`:212`).
- `gate.ErrUnavailable` is the fail-closed error of the policy engine ((read only) `pkg/gate/gate.go:76`).

## ADR check

Each quote is copied from the file in (read only) `docs/adr/`. The "Changed by" chains and the "Current rule" come from (read only) `docs/adr/INDEX.md`.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decisions 4 and 6, "(owner)").

- Decisions that apply: 3, 4, 6, 7, 8.
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12; ADR 385 changes decision 5. Chain to the end: ADR 385 has no further change.
- Current rule: this ADR, with ADR 286, 299, 385.
- Quote (decision 6, owner): "The approvals across all the requirements of one change come from distinct people. The server assigns approvers to requirements with the bipartite matching of ADR 192 decision 6, for jobs, change orders, profile drafts and access requests. So the order of the approvals never changes the result."
- Quote (decision 4, owner): "Defaults that name no approvers merge into one requirement named `default`, with the highest count and the shortest fresh window. A default that names approvers is its own requirement, `default/<scope>`."
- Quote (decision 7): "An eligible approver of a requirement holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) and is in the `approvers` of its rule."
- Quote (decision 8): "A rejection by an eligible approver of any requirement rejects the change."
- Effect: Part A applies decision 6 to jobs. Decision 6 already names jobs, so no new decision is needed for the matching.
- Conflict: decision 8 says "eligible approver", and main lets any holder of `job:approve` reject ((read only) `pkg/job/service_approve.go:251`). This is open question 1. The change keeps main.

**ADR 192: Reviews: approve, request changes, and reject and close** (decision 6 is cited by ADR 225 decision 6).

- Decisions that apply: 6.
- Changed by: ADR 222 supersedes decisions 4 and 5; ADR 225 changes decisions 4 and 5; ADR 227 extends; ADR 299 extends. Decision 6 is not changed.
- Current rule: this ADR, with ADR 222, 225, 227, 299.
- Quote: "The signatures on the current revision meet the requirements only when each signature counts for at most one requirement. One approver who is a member of two groups meets one requirement, not both."
- Quote: "the matcher of change orders finds a maximum matching with augmenting paths. The requirements are met when the matching fills each slot."
- Effect: the job matcher uses the same rule. It does not import `changeorder`, because shared packages stay tool-neutral and the job package must not depend on the change order package.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decisions 2 and 3).

- Decisions that apply: 2, 3, 4 (Part B only).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends; ADR 373 extends decision 1; ADR 385 changes decision 3 and extends decision 2.
- Current rule: this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Quote (decision 4): "A namespace and an approval rule (`freshAuthSeconds`, ADR 225) can set a shorter window or 0. They never set a longer one. The step uses the shortest window that holds for it."
- Effect: Part A does not check a window. Question 4 asks how "the shortest window that holds for it" works when two requirements set different windows.
- Conflict: none for Part A.

**ADR 385: A fresh step is opt-in** (owner-tagged: decisions 1 and 2).

- Decisions that apply: 1, 2, 3.
- Changed by: none. Chain to the end: none.
- Current rule: this ADR.
- Quote (decision 2): "When the setting is off, the server asks a person for no fresh authentication."
- Effect: Part B reads `freshAuthEnabled` at the one place that ADR 385 names. A requirement with `freshAuthSeconds` asks for a step only when the setting is on.
- Conflict: none.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged: all decisions).

- Decisions that apply: 1, 6.
- Changed by: none. Chain to the end: none.
- Current rule: this ADR.
- Quote: "For a job of an untrusted source, an approval gate that reads an unverified fact always asks for its approval." (decision 6; the full text also covers the `unless` of an approval default).
- Effect: Part A adds no default rule and no unless evaluation. Part B must keep this rule (question 5).
- Conflict: none.

**ADR 250: Defense in depth for security code.**

- Changed by: ADR 382 extends decision 3. Current rule: this ADR, with ADR 382.
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: the design has a table of hostile cases for each layer.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged: decision 1, "Stable IDs").

- Changed by: ADR 363 extends decision 2. Current rule: this ADR, with ADR 363.
- Effect: a gate names groups by `GroupIDs`, and the matching keys its slots by the gate name, not by a display string of a group. Question 1 asks for the same rule for `users`.

Security change: each pull request of this change needs the Opus security review (rev2) on its exact head before it is queued.

## Open questions

Open questions: 5

The owner decides each one. Part A does not wait for them. Each answer needs a repository record the same day (an ADR or an ADR amendment).

1. **Who may approve and reject, and how a user is named.** ADR 225 decision 7 says who is eligible, but the field `users` of the approvers holds strings, and no ADR says whether a string is a subject or a stable principal ID. ADR 362 says "stable IDs" for references that can be renamed or made again. ADR 225 decision 8 says an eligible approver rejects, and main lets any holder of `job:approve` reject.
   - Option A: a user entry is the stable principal ID. A rejection needs the eligibility of an approver. Cost: the `users` field changes meaning, the setter must resolve IDs, and a stored rule is read again.
   - Option B: a user entry stays the subject string, as the approval record keeps it today. Rejection stays open to any holder of `job:approve`. Cost: a person who is removed and made again with the same subject keeps the rule.
   - Option C: a user entry is the stable ID, and a rejection stays open to any holder of `job:approve` because a rejection only stops the change.
   - Recommendation: option C. It follows ADR 362 and does not lower safety, because a rejection only stops a change.
   - 3 a.m. summary: do we name approvers by an ID that survives renames, and may any approver reject?
2. **When unit owners resolve for a job.** ADR 225 decision 3 says `unitOwners` read only the context at the scope of the rule or wider. A job has a start time and an approval time, and the context can change between them. The owners are strings such as `group:<name>`, `principal:<ID>` or text (`objectcontext.ParseOwner`).
   - Option A: resolve at the start and store the owner IDs on the gate. Cost: a later owner change does not reach a waiting job.
   - Option B: resolve at each approval. Cost: a reader of the context is needed in `Approve`, and a failed read refuses the approval.
   - Option C: refuse a rule with `unitOwners` on a job until a text owner has no meaning for a job (only `group:` and `principal:` entries count).
   - Recommendation: option B with fail closed on a read error, and only `group:` and `principal:` entries count.
   - 3 a.m. summary: when a job asks "the owners of this unit", do we ask when it starts or when each person approves?
3. **A recheck before dispatch.** Today the approval checks the group membership once, when the person approves. A person may leave a group before the job runs.
   - Option A: the approval time only. This is main for one requirement. The audit entry records the eligibility.
   - Option B: a second check in the transaction of `EventDispatch` ((read only) `pkg/job/store.go:247`). Cost: the store needs a membership reader, and an approver that leaves the group makes the job wait again.
   - Option C: a check in `dispatchJob` of the reconciler, before the store call. Cost: a window between the check and the write.
   - Recommendation: option A for this change. Ask for option B as a separate change, because it changes every job, also with one requirement.
   - 3 a.m. summary: if an approver leaves the group after approving, does the approval still count?
4. **A fresh window with several requirements.** ADR 227 decision 4 says "The step uses the shortest window that holds for it", and a requirement sets `freshAuthSeconds`. With two requirements and one approver, the approval is signed before the matching assigns it.
   - Option A: the shortest window of all requirements of the phase applies to every approval. Cost: a requirement with a long window is stricter than it says.
   - Option B: the window of each requirement applies to the slots of that requirement. The approver has the fresh fact in the approval, and the matching skips a gate whose window the approver does not meet.
   - Option C: no window for jobs until a later change.
   - Recommendation: option B. It keeps each rule as written, and the matching already needs the pairs.
   - 3 a.m. summary: if one rule asks for a sign-in in the last 5 minutes and another asks for nothing, does an old sign-in fill the second only?
5. **When unless is evaluated, and on which facts.** ADR 225 decision 4 gives `unless` over "the facts of `change.planned`". A job has no change order. ADR 353 decision 6 says a gate that reads an unverified fact always asks for approval.
   - Option A: evaluate at the start, on the facts that the start has. An unknown or unverified fact means not exempt.
   - Option B: evaluate when the plan phase ends, on the plan facts. Cost: the job waits first and the exemption comes later.
   - Option C: no `unless` on jobs. Keep the refusal of `DefaultRequirements` ((read only) `pkg/gate/requirements.go:44`).
   - Recommendation: option C for now. The facts of `change.planned` belong to change orders, and a job has none.
   - 3 a.m. summary: may a rule skip approval when a Cedar check says the change is safe, and when does that check run for a job?

Questions considered and answered:

- Does the change force approvals on teams that set no default? No. ADR 225 decision 4 says "With none set, the default is off". The decider reads stored defaults only.
- Is the matching a decision of the owner? Yes, and it is made: ADR 225 decision 6 is tagged "(owner)" and names jobs.
- May one approver fill two requirements? No. ADR 192 decision 6: "One approver who is a member of two groups meets one requirement, not both."
- Where is the cap of the count decided? The structure bounds it at 3 (one default for each scope), and the start checks it.

Drafted by a Claude Sonnet agent against main at `344dec65f4b52ca344ffff02059ededd2e383272`. Every path and line was read on that commit.
