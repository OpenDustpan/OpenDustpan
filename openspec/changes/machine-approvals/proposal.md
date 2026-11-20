---
version: 1
---

Issue: #1158 (the machine part).

Split from the change `several-approval-requirements-one-change`. That change keeps the matching of several requirements and the users, unit owners, fresh windows and `unless` parts. This change holds owner decision 7 of 2026-11-20: a non-person principal may approve a job approval. It is parked: it needs RFC 134 and then a new ADR that the owner accepts, and it has open questions. No task starts before the ADR is on main.

## Why

**Persona: platform engineer.** A platform team runs a release bot as a service principal. The team wants the bot to approve a low-risk job, such as a plan in a sandbox namespace, after the checks of the team pass, so that no person waits at a screen. The team also wants to keep four-eyes for each other job. Today the rule cannot say which token may approve. Main lets any credential that holds `job:approve` approve, and ADR 225 decision 7 says that no token approves. The team needs one rule that names the bot, and that stays closed for every other token.

The user path this change must make work:

1. **API.** An administrator names the service principal in an approval rule or an approval default, in the field `machineIds` of `approvers`, by its stable principal ID.
2. **API.** A person starts a job that the rule guards. The job waits in `awaiting-approval`.
3. **API.** The service principal sends `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` with `{"verdict": "approve"}` and its access token. The approval fills one slot, and the audit entry and the job response mark it as a machine approval.
4. **Docs page.** `docs/guides/approval-rules.md` has one section on `machineIds`: who may approve with a token, the four conditions, and the one command to list the entries.

Errors the person may hit, and the one command that helps. Each row is added by this change:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 on an approval by a service principal that no rule names | The rule does not name this principal in `machineIds`. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| 403 on an approval by a token whose requirement sets a fresh window | A machine gives no fresh step, so it fills no requirement that has a window. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| 403 on an approval of a job that the same principal started | A machine never approves a job that it started. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| The save of a rule that names a principal by name | The save fails with `gate.ErrInvalid`: `machineIds` holds stable IDs. | `GET /api/server/namespaces/{namespace}/principals` |

Evidence for the claims above:

- Main follows neither ADR fully. `checkApprover` refuses an agent and a subject that the policy does not count, and nothing else by kind (`pkg/job/service_approve.go:208`). `ActsAsPerson` exists (`pkg/identity/identity.go:136`), and the job code does not call it. So a service principal, a job token and an access token that hold `job:approve` approve a job today.
- The starter refusal reads `StartedBy` and `AllowSelf` only (`pkg/job/service_approve.go:225`, `pkg/job/gate.go:266`). The job holds `StartedBy` (`pkg/job/job.go:456`).
- A token record has `CreatedBy` (`pkg/accesstoken/accesstoken.go:159`). A principal record has no creator or owner field (`pkg/principal/principal.go:180`).
- `Approvers` has no field for a non-person (`pkg/gate/rules.go:147`). The stored approval has no mark of the credential (`pkg/job/gate.go:172`), the decision record has none (`pkg/gate/record.go:91`), and the audit entry has `TokenID` and `PrincipalKind` (`pkg/audit/audit.go:437`).
- The credential kind is on the principal: `Credential` (`pkg/identity/identity.go:75`). `ActsAsPerson` is true for the access token of a person, so it cannot be the test of this rule. The test is the credential and the kind.

## What Changes

The text of the future ADR is in RFC 134 (`docs/rfc/0134-machine-approvers-for-job-approvals.md`). This change implements the RFC option 2 as the PM proposes it. An item that the owner has not decided is an open question below, and its task waits for the answer.

- `pkg/gate/rules.go`: `Approvers` (`:147`) gets `MachineIDs`, a list of principal IDs (prefix `prn-`). The config service checks at save that each ID is a stored principal of kind `service` or `person`, and refuses a name with `gate.ErrInvalid`. `groups`, `users` and `unitOwners` never admit a non-person.
- `pkg/job/gate.go`: `Gate` gets `MachineIDs`. `Approval` (`:172`) gets `Machine bool` and `Credential`. The job body codec keeps them, so no SQL baseline changes. The client never sets them.
- `pkg/gate/jobgate/jobgate.go`: `gatesOf` copies `MachineIDs` to the gate.
- `pkg/job/service_approve.go`: `checkApprover` (`:208`) classifies the request by credential and kind. A person in a session keeps the checks of main. An access token of a person, or of a service principal, passes only when the ID of the principal is in `MachineIDs` of the gate. A job token, a trust token and an agent are refused. A machine approval ignores `AllowSelf` and `authorMayApprove`.
- `pkg/job/service_approve.go` (`checkApproval`, `:83`): `For` of a machine approval holds only the gates that store no fresh window. When no gate is left, the approval is refused.
- `pkg/job/match.go`: the matcher counts an approval with `Machine` true only on a gate that has `MachineIDs` that hold the subject and no stored window (fail closed on a stored approval from a restored backup).
- `approvalEntry` (`:391`) writes the changes `machine` and `credential`. `Signature` (`pkg/gate/record.go:91`) gets `Machine bool` and `TokenID`. A refusal of a machine approval writes an audit entry of the outcome `denied` with the cause.
- `internal/api/jobs/serve.go` and the approval rule routes: the field `machineIds` of `approvers`, and `machine` on `approvals[]`. The API description and the clients are generated again.
- `docs/guides/approval-rules.md`: the section on `machineIds`.

Out of scope:

- Change orders and profile drafts. `changeorder:approve` and `profile:approve` keep ADR 225 decision 7.
- A job token, a trust token of a workload and an agent. They stay refused (no owner decision, ADR 103 decision 7, ADR 128 decision 10, ADR 299 decision 35, ADR 385 decision 7).
- Rejection. Any holder of `job:approve` may reject, as on main and as `several-approval-requirements-one-change` records.
- A creator field on the principal record (see open question 1). It is a separate change.
- The matching of several requirements, users by ID, unit owners, fresh windows and `unless`. They stay in `several-approval-requirements-one-change`.

Depends on:

- `several-approval-requirements-one-change`. This change reads `Gate.Name`, `Approval.For`, the matcher in `pkg/job/match.go`, the gate list `UserIDs` of task 5.1 (the same save check and the same "no ID counts no one" row) and the fresh window of each gate of task 5.4. Task 5.4 and the machine rule touch the same lines of `checkApproval` (`pkg/job/service_approve.go:83`), so this change starts after task 5.4 is on main.
- RFC 134, and the ADR that follows it. The owner accepts the ADR. No task starts before it is on main (rule of the repository: an owner decision becomes a record first).

## Owner decisions folded in

Owner decision 7 (owner, 2026-11-20). A non-person principal (a service principal, or the access token of a person) may approve a job approval under four conditions:

1. **Named by stable ID.** The approval rule names it explicitly by stable ID. A group, or `job:approve` alone, does not count.
2. **No self-approval.** It never approves a job that it started or authored.
3. **No fresh window.** It cannot meet a requirement that sets a fresh window.
4. **Marked.** The audit entry and the decision record mark the approval as a machine approval.

## Usability

- A rule shows its machine approvers in `machineIds`, so an operator sees who may approve with a token without reading code.
- A refused machine gets the cause, not a general 403: not named, a window, or the starter.
- A person can end the path at once: remove the ID from the rule, or revoke the token. The audit entry names the token.
- The job response shows `machine: true` on an approval, so a reviewer sees that no person decided at a screen.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `checkApprover` refuses an agent (`a.Agent != ""`, `pkg/job/service_approve.go:209`) and a subject that the policy does not count. It does not read the kind or the credential (`:208`).
- The starter is refused when no gate sets `AllowSelf` (`pkg/job/service_approve.go:225`). `Gate.counts` does the same (`pkg/job/gate.go:266`).
- A rejection by any holder of `job:approve` rejects the job (`pkg/job/service_approve.go:251`).
- `Principal.Credential` is empty or one of the credential kinds. An empty credential counts as an access token (`pkg/identity/identity.go:75`). `ActsAsPerson` is false for a job token and a trust token only (`:136`, `:137`).
- `FreshAuth` is `Set` and `Seconds` on a requirement (`pkg/gate/rules.go:77`).

## ADR check

Each quote is copied from the file in `docs/adr/` on main at `331a87f544490e1ab08625d6a7ce75d28ab4393b`. The "Changed by" chains come from `docs/adr/INDEX.md`, and `rg` over `docs/adr` and `docs/rfc`.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decisions 4 and 6).

- Decisions that apply: 6, 7.
- Changed by: ADR 286 changes (the words catalog and promotion, no effect on decision 7); ADR 299 changes decisions 1, 7 and 12 ("`authorMayApprove` does not apply to a review request, mode 1 counts reviews on the Git host, and the binding level replaces signature carry-over"; no rule on the kind of principal that approves a job); ADR 385 changes decision 5, not decision 7. No ADR changes decision 7 for the kind of the approver. Current rule: this ADR, with ADR 286, 299, 385.
- Quote (decision 7): "An eligible approver of a requirement holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) and is in the `approvers` of its rule. An author is eligible only when `authorMayApprove` is `true`. An approver is a person in a session. A service principal, a job token and an access token never approve, also when they hold the approve action. An agent never approves (ADR 128 decision 10 stays)."
- Quote (decision 6, owner, from line 102): "... one change come from distinct people. The server assigns approvers to requirements with the bipartite matching of ADR 192 decision 6 ..."
- Effect: owner decision 7 changes the sentences "An approver is a person in a session." and "A service principal, a job token and an access token never approve, also when they hold the approve action." for a job approval. They hold for `changeorder:approve`, `profile:approve`, a job token, a trust token and an agent. A machine approval fills one slot, and one subject fills one slot (decision 6 extended).
- Conflict, open: this is a change of an owner-tagged ADR decision, so it needs RFC 134, then a new ADR that the owner accepts. No task starts before that.

**ADR 385: A fresh step is opt-in** (owner-tagged: decisions 1 and 2).

- Decisions that apply: 2, 3, 7.
- Changed by: none. Current rule: this ADR.
- Quote (decision 2): "With the setting off, an access token that holds the grant may approve a `destroy` (owner, 2026-11-15). The handler, the service and the store each read the same effective value. None of them keeps a rule of its own."
- Quote (decision 3): "An access token never gives one."
- Quote (decision 7): "ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has. The setting removes the step for a person. It gives an agent no new action."
- Effect: the first sentence of decision 2 holds only for an access token that a rule names, and only for a requirement with no stored fresh window (narrowing, open question 6). The second and third sentences, and decisions 3 and 7, stay.
- Conflict, open: the narrowing of an owner-tagged sentence needs the owner's word (open question 6).

**ADR 362: Owner decisions of 8 November 2026** (owner-tagged: decision 1, "Stable IDs").

- Changed by: ADR 363 extends decision 2. Current rule: this ADR, with ADR 363.
- Effect: `machineIds` holds stable principal IDs. A principal that someone removes and makes again has a new ID, so it never inherits the entry.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decisions 2 and 3).

- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends; ADR 373 extends decision 1; ADR 385 changes decision 3 and extends decision 2. Current rule: this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Effect: a machine approval has no fresh step. It extends decision 2, the list of the steps.
- Conflict: none.

**ADR 378: The binding of an uploaded plan.**

- Quote (decision 4): "The person who uploaded a plan never approves it."
- Effect: the machine rule for an author of a plan reads the same record when the upload change adds it. Main has no author field on the job (`pkg/job/job.go`, `pkg/job/gate.go`, `pkg/job/service_approve.go`).
- Conflict: none.

**ADR 250: Defense in depth for security code.** Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed." The design has a table of hostile cases for each layer.

Security change: each pull request of this change needs the Opus security review (rev2) on its exact head before it is queued.

## Open questions

Open questions: 6, parked

The owner decides, because these are security semantics and the first and sixth change owner-tagged words. The PM takes no provisional answer. The questions and the recommendations are those of RFC 134. Each task that depends on a question names it. The RFC also needs its own acceptance as an ADR before any task starts.

1. **Token created by the starter (the self-approval gap).** Condition 2 names "started or authored" (`StartedBy` and the author of a plan). A person starts a job, makes a token for a service principal, and the principal approves: one person fills two roles, and neither word names the principal. Cost of the gap: four-eyes that one person defeats alone.
   - Context: ADR 388 (owner, 2026-11-20) already treats the maker of a service token as a stored fact: the server revokes the token when its maker loses the act-as path. Option A reads the same fact.
   - Option A: refuse a machine approval when `Token.CreatedBy` of the approving token equals `StartedBy`. Cost: a creator check that does not cover a principal that a person made and a second person runs, because no principal field links a creator.
   - Option B: A, and a creator field on the principal record, as a separate change. Cost: a schema change and one more change to land first.
   - Option C: no extra rule, as the owner worded it. Cost: the gap stays and the docs must say so.
   - Recommendation: A, and file a bug issue for B as a separate change.
   - 3 a.m. summary: may a person start a job and approve it through a bot that the person made?
2. **A stored window with the setting off.** "Sets a fresh window" can mean the stored `freshAuthSeconds` or the enforced one.
   - Option A: the stored value. A machine never fills the requirement, and a later turn-on of `freshAuthEnabled` changes no job.
   - Option B: the enforced value. A machine fills a requirement with a stored window while the setting is off. Cost: the same job changes meaning when the setting changes.
   - Recommendation: A (ADR 385 decision 6 keeps the stored value with `"enforced": false`).
   - 3 a.m. summary: when a rule asks for a fresh step but the installation has the step turned off, may a bot approve?
3. **Machines fill every slot.** Two named machines may fill a count of 2, and no person looks at the change. The decision does not forbid it.
   - Option A: allow it, and say in the ADR that machine approval is the choice of the rule author. Cost: a rule author may build a rule with no person.
   - Option B: a rule field `minPersons`. Cost: a new field in each layer.
   - Recommendation: A. A rule that wants a person names a group or `users` for one requirement and `machineIds` for none.
   - 3 a.m. summary: may a rule let bots alone approve a change?
4. **Name a token or a principal.** The rule names the principal ID, so a rotated token keeps working. Naming the token ID limits the entry to one token, at the cost of a rule edit at each rotation.
   - Recommendation: the principal ID (the owner says "stable ID"), and the audit entry names the token.
   - 3 a.m. summary: after a token rotation, does the bot still approve without a rule edit?
5. **Where the field lives.** The rule field `machineIds`, the gate field and the API field (`approvers` of an approval rule and an approval default, the console form). Options: a separate list `machineIds` (recommended; a group can never admit a machine, and the type shows it), or a flag on each user or group entry (one entry with two meanings; on a group entry the flag would name a set, not an ID, and condition 1 forbids that). The tool-neutral APIs may break before 1.0, so the approval API has no compatibility need.
   - 3 a.m. summary: in which field does an administrator list a bot that may approve?
6. **The `destroy` approval and ADR 385 decision 2 (narrowing).** The owner decision of 2026-11-15 says an access token that holds the grant approves a `destroy` with the setting off. Under decision 7 it does so only when named, and only with no stored window.
   - Option A: the narrowing. The ADR says so. Cost: an automation that approves a `destroy` with an unnamed token stops working.
   - Option B: the earlier rule (any token that holds the grant) stays for a `destroy`. Cost: a group or `job:approve` alone admits a token, which condition 1 forbids.
   - Recommendation: A. The words of decision 7 say "A group or `job:approve` alone does not count".
   - 3 a.m. summary: may any token with the approve right still approve a `destroy` when the fresh step is off, or only a token that the rule names?

Drafted by a Claude Sonnet agent from RFC 134 and the task table of `several-approval-requirements-one-change`. Every path and line was read on main at `331a87f544490e1ab08625d6a7ce75d28ab4393b`.
