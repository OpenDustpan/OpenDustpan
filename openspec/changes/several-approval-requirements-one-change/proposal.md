---
version: 8
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
| The start of a job fails because two defaults apply (exists on main: (read only) `pkg/gate/jobgate/jobgate.go:168`) | After this change, the start succeeds for requirements that name groups, users or unit owners. A default with an `unless` body still fails the start with the same error, because a job has no `unless` for now (owner, 2026-11-20). A default with `freshAuthSeconds` fails the start until task 5.4 lands. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-default` |
| 403 on an approval: the subject is not a member of a group of any requirement of the phase (exists on main in `checkGroups`, (read only) `pkg/job/service_approve.go:253`) | After this change, the answer names the requirements of the phase that the subject cannot fill. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| 409 `job.already-approved` (exists: (read only) `internal/errcode/catalog_job.go:9`) | The caller approved this phase already. One subject fills one slot. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- A job holds one gate per phase kind, with one count. Proof: `Decision.Gates` is `map[PhaseKind]Gate` ((read only) `pkg/job/gate.go:431`) and `PolicyGates` copies one gate for each kind ((read only) `pkg/job/gate.go:129`).
- The decider refuses more than one requirement. Proof: `gateOf` returns an error that wraps `gate.ErrUnavailable` when `len(reqs) > 1` ((read only) `pkg/gate/jobgate/jobgate.go:168`). The test `TestDecideFailsClosed` fixes this refusal ((read only) `pkg/gate/jobgate/jobgate_test.go:426`).
- `Gate.Status` and `GatesStatus` count each gate alone, with no rule that a person fills one slot across gates ((read only) `pkg/job/gate.go:236`, (read only) `pkg/job/gate.go:295`). So a phase with two gates would count one person twice.
- `checkGroups` demands that the approver is in the groups of every gate that names groups ((read only) `pkg/job/service_approve.go:253`). With several gates, one approver could never be a member of two disjoint groups, so a phase with two group gates would never open.
- The approval state is in the JSON body of the job row, so the SQL baselines do not change. Proof: `gateBody` and `approvalBody` in (read only) `pkg/store/internal/jobstore/body.go:127` and `:144` are the one codec of both dialects.
- The jobs API already returns the gates and the approvals of each phase ((read only) `internal/api/jobs/serve.go:92`, `:412`, `:419`).
- The approval defaults give at most 3 requirements for one unit. Proof: `UnitChain` returns the namespace, the collection and the unit scopes ((read only) `pkg/gate/scope.go:459`), and each scope holds one default ((read only) `pkg/gate/rules.go:298`). `DefaultRequirements` turns each default into one requirement or merges it into `default` ((read only) `pkg/gate/requirements.go:30`).


## What Changes

The change has two parts. Part A is buildable now, because ADR 225 decision 6 and ADR 192 decision 6 decide it. Part B is decided by the owner (2026-11-20, see "Owner decisions folded in"), except the one shape that waits for the open question.

**Part A: several group requirements on one job, with distinct people.**

- `pkg/job/gate.go`: `Gate` gets a NEW field `Name`, the name of its requirement (`default`, `default/<scope key>`). `Decision.Gates` (exists, `map[PhaseKind]Gate`, `:431`) becomes `map[PhaseKind][]Gate` (NEW type), and `PolicyGates` (`:129`) copies each gate of a kind. `Approval` gets a NEW field `For`: the names of the gates whose approvers held the approver when the approver signed. `GatesStatus` (`:295`) becomes a maximum matching: each gate has `Approvals` slots, and one subject fills one slot of one gate. A new file `pkg/job/match.go` holds the matching, with augmenting paths over at most 3 gates.
- `pkg/job/service_approve.go`: `checkApprover` (`:208`) and `checkGroups` (`:253`) check each gate alone and set the NEW field `For` of the approval to the gates that hold the approver. An approval whose `For` is empty is refused with `identity.ErrDenied`. `record` (`:347`) and `approvalEntry` (`:391`) write the names of `For` to the audit entry. `waitingGates` (`:174`) stays.
- `pkg/job/reconcile.go:372` and `pkg/job/service_start.go:735`: both call `GatesStatus` and need no code change. The tests prove that they use the matching.
- `pkg/job/service_start.go:439`: the start refuses a decision with more than 3 gates for a phase, with `ErrInvalid` (defense in depth, see the design).
- `pkg/gate/jobgate/jobgate.go`: `gateOf` (`:167`) becomes `gatesOf`. It returns one `job.Gate` for each requirement, with the name set. In Part A it keeps the refusal, with the same text, for a requirement that names users or unit owners, sets `freshAuthSeconds`, or has groups saved with no group IDs; Part B removes the first two refusals one at a time. `Decide` (`:73`) sets the `BindPlan` field on every gate of a changing phase.
- `pkg/store/internal/jobstore/body.go`: `gateBody` gets `name`, `approvalBody` gets `for`. The stores of both dialects share this codec, and the JSON column `body` of the jobs table holds this state, so the baselines `pkg/store/sqlite/migrations/` and `pkg/store/postgres/migrations/` do not change and no migration is added.
- `internal/api/jobs/serve.go:130-145`: `gateResponse` gets `name` and `filled`, `approvalResponse` gets `for`. The API description and the clients are generated again with `make generate` and `make clients-generate`.
- `docs/guides/approval-rules.md`: the section "One requirement only" (`:31-37`) is replaced. `docs/guides/jobs.md` (approve section, `:77`) shows the new response fields.

**Part B: users, unit owners, fresh windows and the refusal of `unless` (decided).** Each item follows an owner decision. The tasks are in `tasks.md`, section 5.

- Users by stable ID. An approver entry in `users` is the stable principal ID, with the name kept for display only, as `groups` and `groupIds` are. A gate holds `UserIDs`. A user entry with no ID counts no one (fail closed).
- Unit owners resolve at each approval. `Approve` reads the owners of the unit from the context at the scope of the rule or wider, and only `group:` and `principal:` entries count. A read error refuses the approval.
- An approval counts as of the approval time. `For` is the only record of eligibility. Nothing checks the approver again at dispatch.
- Each requirement keeps its own fresh window. A requirement with `freshAuthSeconds` becomes a gate with its own window, and an approval fills only the gates whose window its fresh step meets. This task waits for three other changes (see "Depends on").
- No `unless` on a job for now. `DefaultRequirements` keeps its refusal ((read only) `pkg/gate/requirements.go:44`), and a test and the guide fix it.
- Rejection stays open to every holder of `job:approve`, as on main.

Out of scope:

- Who may approve by kind of principal: a service principal, a job token and an access token. This is the open question. Until the owner answers, the job code keeps what main does: it refuses an agent and no other kind.
- Withdrawal of an approval. No ADR gives a withdrawal verdict, and the route `POST .../approvals` accepts only `approve` and `reject` (`internal/api/jobs/approve.go`). A withdrawal needs an ADR for the verdict, who may withdraw, and what the audit entry says. It is a new capability and gets its own change.
- A recheck of the group membership at dispatch. The owner decided that an approval counts as of the approval time, and that a recheck is a separate, later change. It would change every job, also with one requirement.
- A cap of 10 requirements and a 429 refusal. The cap has no counting point: the defaults give at most 3 requirements, so the structure bounds the count. The check of 3 in the start is the guard.
- Ownership rules (`owners/<id>` requirements) on jobs. Main does not read ownership rules in `jobgate`, and this change does not add them.
- Named approval rules on jobs (`approval:<ID>` gates). Jobs read approval defaults only.
- Change orders. They have their own matcher (the matcher of change orders, ADR 192 decision 6).
- Gate configuration routes, the simulate route and the console run page.

Depends on: none for Part A and for Part B except the fresh window.

The fresh window (task 5.4) starts after the changes session-fresh-cookie, fresh-auth-enabled-setting and fix-destroy-approval-fresh-auth are on main: they give the fresh-authentication fact.

Not a dependency: the change approval-defaults-take-effect-live. Its premise (the decider is wired nowhere) is false on main: (read only) `pkg/dustpan/jobservice.go:45` builds `jobgate.New`. Mark that change stale in its own pull request.
- The changes fresh-authentication-window-0-turns and gate-evaluation-must-apply-dev are not dependencies of Part A.

## Owner decisions folded in

The owner answered four of the five first questions and decided that the fifth stays out. Each answer needs a repository record the same day (an ADR or an ADR amendment). Task 5.7 makes it.

| First question | Owner decision (2026-11-20) | Effect in this change |
| --- | --- | --- |
| 1. Who may approve and reject, and how a user is named | Name approvers by stable ID. Any `job:approve` holder may reject. | Option C of the first draft. `UserIDs` on the gate. Rejection stays as on main. |
| 2. When unit owners resolve | Resolve unit owners at each approval, and fail closed. Only `group:` and `principal:` entries count. | Option B. `Approve` reads the context. A text owner counts no one. |
| 3. A recheck before dispatch | An approval counts as of the approval time. A recheck at dispatch is a separate, later change. | Option A. `For` is written once, at the approval. |
| 4. A fresh window with several requirements | Each requirement keeps its own fresh window. | Option B. Task 5.4. |
| 5. `unless` on a job | No `unless` on jobs for now. | Option C. The refusal stays. |

## Usability

- The job read response lists each requirement with its count and its filled slots, so a developer sees what waits before asking anyone.
- A refused approver gets the names of the requirements that they cannot fill, not a general 403.
- The start of a job with defaults in two scopes works the first time from the docs page, whether the defaults name groups, users or unit owners.
- A default with an `unless` body fails the start with the one command that helps: remove the `unless` body from the default.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `Gate` is one requirement: one count, one list of groups ((read only) `pkg/job/gate.go:89`).
- A phase holds `[]Gate` (`phase.Gates`, (read only) `pkg/store/internal/jobstore/body.go:164`), but the policy fills one gate for each kind (`PolicyGates`, (read only) `pkg/job/gate.go:129`).
- `Approve` reads the waiting gates, checks the approver and records the verdict ((read only) `pkg/job/service_approve.go:38`, `:174`, `:208`, `:347`).
- `record` picks `EventApproved`, `EventApprovalRecorded` or `EventRejected` from `GatesStatus` ((read only) `pkg/job/service_approve.go:357`).
- The reconciler dispatches a queued job when `GatesStatus` is open ((read only) `pkg/job/reconcile.go:372`). The store checks the plan binding, the lock, the busy unit and the destroy proof in `Store.Apply` for `EventDispatch` ((read only) `pkg/job/store.go:247`, `checkDestroyProof` in (read only) `pkg/store/internal/jobstore/events.go`).
- A rejection by any holder of `job:approve` rejects the job. It needs no group check ((read only) `pkg/job/service_approve.go:251`).
- `checkApprover` refuses an agent (`a.Agent != ""`) and a subject that the policy does not count ((read only) `pkg/job/service_approve.go:208`). It does not read the kind or the credential of the principal. `ActsAsPerson` exists ((read only) `pkg/identity/identity.go:136`), and the job code does not call it.
- The route `POST .../jobs/{job_id}/approvals` declares `job:read` and `job:approve` on the unit of the job ((read only) `internal/api/jobs/routes.go:157`).
- `DefaultRequirements` refuses a default with an `unless` body ((read only) `pkg/gate/requirements.go:44`). `Unless` is a field of `ApprovalDefault` ((read only) `pkg/gate/rules.go:310`), not of `ApprovalRule` (`:212`).
- `Approvers` holds `Groups` and `GroupIDs` (name for display, ID for identity), `Users` as strings and `UnitOwners` as a flag ((read only) `pkg/gate/rules.go:147`). `Requirement.FreshAuth` holds `Set` and `Seconds` ((read only) `pkg/gate/record.go:109`, `pkg/gate/rules.go:83`).
- `gate.ErrUnavailable` is the fail-closed error of the policy engine ((read only) `pkg/gate/gate.go:76`).

## ADR check

Each quote is copied from the file in (read only) `docs/adr/` on main at `65b8436656e64e5952d42713eb968f03c849b83d`. The "Changed by" chains and the "Current rule" come from (read only) `docs/adr/INDEX.md`.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decisions 4 and 6, "(owner)").

- Decisions that apply: 3, 4, 6, 7, 8.
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12; ADR 385 changes decision 5. Chain to the end: ADR 385 has no further change. ADR 299 "Changes ADR 225 decisions 1, 7 and 12: `authorMayApprove` does not apply to a review request, mode 1 counts reviews on the Git host, and the binding level replaces signature carry-over." That line gives no rule on the kind of principal that approves a job, so decision 7 stands for jobs.
- Current rule: this ADR, with ADR 286, 299, 385.
- Quote (decision 6, owner): "The approvals across all the requirements of one change come from distinct people. The server assigns approvers to requirements with the bipartite matching of ADR 192 decision 6, for jobs, change orders, profile drafts and access requests. So the order of the approvals never changes the result."
- Quote (decision 4, owner): "Defaults that name no approvers merge into one requirement named `default`, with the highest count and the shortest fresh window. A default that names approvers is its own requirement, `default/<scope>`."
- Quote (decision 7): "An eligible approver of a requirement holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) and is in the `approvers` of its rule. An author is eligible only when `authorMayApprove` is `true`. An approver is a person in a session. A service principal, a job token and an access token never approve, also when they hold the approve action. An agent never approves (ADR 128 decision 10 stays)."
- Quote (decision 8): "A rejection by an eligible approver of any requirement rejects the change."
- Effect: Part A applies decision 6 to jobs. Decision 6 already names jobs, so no new decision is needed for the matching. Decision 7 on the kind of the approver is the open question.
- Conflict, answered: decision 8 says "eligible approver", and main lets any holder of `job:approve` reject ((read only) `pkg/job/service_approve.go:251`). The owner decided that any `job:approve` holder may reject (2026-11-20). Task 5.7 records an amendment of the words of decision 8 for jobs.
- Conflict, open: decision 7 says an access token never approves, and ADR 385 decision 2 says the opposite for a `destroy` with the setting off. See the open question.

**ADR 192: Reviews: approve, request changes, and reject and close** (decision 6 is cited by ADR 225 decision 6).

- Decisions that apply: 6.
- Changed by: ADR 222 supersedes decisions 4 and 5; ADR 225 changes decisions 4 and 5; ADR 227 extends; ADR 299 extends. Decision 6 is not changed.
- Current rule: this ADR, with ADR 222, 225, 227, 299.
- Quote: "The signatures on the current revision meet the requirements only when each signature counts for at most one requirement. One approver who is a member of two groups meets one requirement, not both."
- Quote: "the matcher of change orders finds a maximum matching with augmenting paths. The requirements are met when the matching fills each slot."
- Effect: the job matcher uses the same rule. It does not import `changeorder`, because shared packages stay tool-neutral and the job package must not depend on the change order package.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decisions 2 and 3).

- Decisions that apply: 2, 3, 4 (task 5.4).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends; ADR 373 extends decision 1; ADR 385 changes decision 3 and extends decision 2.
- Current rule: this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Quote (decision 4): "A namespace and an approval rule (`freshAuthSeconds`, ADR 225) can set a shorter window or 0. They never set a longer one. The step uses the shortest window that holds for it."
- Effect: the owner decided that each requirement keeps its own fresh window (2026-11-20). "The window that holds for it" is the window of the requirement that the approval fills. The one window of the installation is the upper bound for each requirement, and a requirement never sets a longer one.
- Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged: decisions 1 and 2).

- Decisions that apply: 1, 2, 3, 7.
- Changed by: none. Chain to the end: none.
- Current rule: this ADR.
- Quote (decision 2): "When the setting is off, the server asks a person for no fresh authentication." The same decision says: "With the setting off, an access token that holds the grant may approve a `destroy` (owner, 2026-11-15)."
- Quote (decision 3): "An access token never gives one."
- Quote (decision 7): "ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has. The setting removes the step for a person. It gives an agent no new action."
- Effect: task 5.4 reads `freshAuthEnabled` at the one place that ADR 385 names. A requirement with `freshAuthSeconds` asks for a step only when the setting is on, and the job response shows the stored value as not enforced when the setting is off.
- Conflict, open: the sentence of decision 2 about an access token against decision 7 of ADR 225. See the open question.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged: all decisions).

- Decisions that apply: 1, 6.
- Changed by: none. Chain to the end: none.
- Current rule: this ADR.
- Quote: "For a job of an untrusted source, an approval gate that reads an unverified fact always asks for its approval." (decision 6; the full text also covers the `unless` of an approval default).
- Effect: this change adds no default rule and no `unless` evaluation. A job has no `unless` for now (owner, 2026-11-20), so the rule has nothing to read.
- Conflict: none.

**ADR 250: Defense in depth for security code.**

- Changed by: ADR 382 extends decision 3. Current rule: this ADR, with ADR 382.
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: the design has a table of hostile cases for each layer.

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged: decision 1, "Stable IDs").

- Changed by: ADR 363 extends decision 2. Current rule: this ADR, with ADR 363.
- Effect: a gate names groups by `GroupIDs` and users by `UserIDs`, and the matching keys its slots by the gate name, not by a display string. The owner applied the same rule to users (2026-11-20).

Security change: each pull request of this change needs the Opus security review (rev2) on its exact head before it is queued.

## Open questions

Open questions: 1, parked

Part A and the other tasks of Part B do not depend on the question. Only task 5.6 does. The change stays parked until the PM lifts the park, and the owner decides the question. The answer needs a repository record the same day (an ADR or an ADR amendment).

1. **May a service principal, a job token or an access token approve a job?** ADR 225 decision 7 says "A service principal, a job token and an access token never approve, also when they hold the approve action." ADR 385 decision 2 (owner, 2026-11-15) says "With the setting off, an access token that holds the grant may approve a `destroy`." Main follows neither fully: `checkApprover` refuses an agent only, and the route lets any holder of `job:approve` approve, with any credential ((read only) `pkg/job/service_approve.go:208`, `pkg/identity/identity.go:136`). The two ADRs cannot both hold for a `destroy` approval.
   - Option A: ADR 225 decision 7 holds for every approval. Only a person in a session approves. The job code refuses a service principal, a job token and an access token, with `identity.Principal.ActsAsPerson` and the credential kind, and writes the refusal to the audit log. Cost: an automation that approves with an access token stops working, and the sentence of ADR 385 decision 2 about an access token and a `destroy` changes (an owner-tagged decision, so it needs the owner's word). A `destroy` approval then always has a person, with or without a fresh step.
   - Option B: ADR 385 decision 2 holds. An access token of a person that holds `job:approve` approves, and a service principal, a job token and an agent never approve. Cost: a stolen token approves a change, and the approver is the token holder, not a person at a screen. The audit entry names the token ID. Four-eyes holds only as far as the token holders are different people.
   - Option C: the narrowest reading of both. An access token of a person may approve a `destroy` only, and only when the setting `freshAuthEnabled` is off. Every other approval needs a person in a session. Cost: two rules for one action, and a second path to test in every layer.
   - Recommendation: option A (lead recommendation: ADR 225 decision 7 wins). Four-eyes needs two people who each decide at a screen. ADR 385 decision 7 says that the setting "removes the step for a person" and gives no new right to anything else, so the sentence about an access token in decision 2 can be read as the grant check only. ADR 299 decision 35 says "Agents never approve, on either side (owner)", and option A keeps one rule for all approvers. Option A also needs no second code path.
   - 3 a.m. summary: when a script holds an access token with the approve right, may it approve a job, or must a person click approve?

Questions considered and answered:

- Does the change force approvals on teams that set no default? No. ADR 225 decision 4 says "With none set, the default is off". The decider reads stored defaults only.
- Is the matching a decision of the owner? Yes, and it is made: ADR 225 decision 6 is tagged "(owner)" and names jobs.
- May one approver fill two requirements? No. ADR 192 decision 6: "One approver who is a member of two groups meets one requirement, not both."
- Where is the cap of the count decided? The structure bounds it at 3 (one default for each scope), and the start checks it.
- How is a user named in `approvers`? By stable principal ID. Owner, 2026-11-20.
- May any holder of `job:approve` reject? Yes. Owner, 2026-11-20.
- When do unit owners resolve for a job? At each approval, with fail closed, and only `group:` and `principal:` entries count. Owner, 2026-11-20.
- Does an approval count after the approver leaves the group? Yes. An approval counts as of the approval time. A recheck at dispatch is a separate, later change. Owner, 2026-11-20.
- How do two fresh windows work on one job? Each requirement keeps its own. Owner, 2026-11-20.
- Does a job get `unless`? No, not for now. Owner, 2026-11-20.

Drafted by a Claude Sonnet agent against main at `344dec65f4b52ca344ffff02059ededd2e383272`. Folded with the owner decisions of 2026-11-20 and read again on main at `65b8436656e64e5952d42713eb968f03c849b83d`. Every path and line was read on that commit.
