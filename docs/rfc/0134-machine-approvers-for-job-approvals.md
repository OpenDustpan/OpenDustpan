---
rfc: 134
title: Machine approvers for job approvals
status: discussing
created: 2026-11-20
---

# RFC 134. Machine approvers for job approvals

Status: discussing. The owner has decided the question (owner, 2026-11-20, see "Decision of the owner"). This RFC does not accept the decision as an ADR. It records the decision, the exact ADR words that it changes, the effects, and the points that the decision leaves open. The ADR that follows is a separate pull request, and the owner accepts it. This RFC edits no ADR.

## Problem

[ADR 225](../adr/0225-approval-rules-approval-defaults-and-ownership-rules.md) decision 7 says that only a person in a session approves. [ADR 385](../adr/0385-a-fresh-step-is-opt-in.md) decision 2 (owner, 2026-11-15) says the opposite for one case: with `freshAuthEnabled` off, an access token that holds the grant may approve a `destroy`. Both are owner-tagged decisions. They cannot both hold for a `destroy` approval.

Main follows neither fully. `checkApprover` refuses an agent and a subject that the policy does not count, and nothing else by kind (`pkg/job/service_approve.go:208`). `identity.Principal.ActsAsPerson` exists (`pkg/identity/identity.go:136`), and the job code does not call it. A service principal, a job token and an access token that hold `job:approve` can approve a job today. This is code that runs ahead of the decisions. It is not a decision.

The change `several-approval-requirements-one-change` (`openspec/changes/`) parks task 5.6 on this question ("Open questions: 1, parked" in its proposal). The owner answered on 20 November 2026. The answer changes an owner-tagged ADR, so an RFC comes first (ADR 15, and "If a change contradicts an ADR, write an RFC first" in `CLAUDE.md`).

## Decision of the owner

Owner decision 7 (owner, 2026-11-20). A non-person principal may approve a job approval. A non-person principal is a service principal, or the access token of a person. It may approve under four conditions:

1. **Named by stable ID (owner, 2026-11-20).** The approval rule names it explicitly by its stable ID. A group, or the action `job:approve` alone, does not count.
2. **No self-approval (owner, 2026-11-20).** It never approves a job that it started or authored.
3. **No fresh window (owner, 2026-11-20).** It cannot meet a requirement that sets a fresh window.
4. **Marked (owner, 2026-11-20).** The audit entry and the decision record mark the approval as a machine approval.

The decision says "a job approval". It names no other approver kind. This RFC reads it in this way: the scope is the job approval (`job:approve`). A job token, a trust token of a workload and an agent are not in it, and they stay refused (see "Scope").

## The ADR words that change

### ADR 225 decision 7

Verbatim, from `docs/adr/0225-approval-rules-approval-defaults-and-ownership-rules.md` lines 106 to 113:

> 7. **Who may approve.** An eligible approver of a requirement holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) and is in the `approvers` of its rule. An author is eligible only when `authorMayApprove` is `true`. An approver is a person in a session. A service principal, a job token and an access token never approve, also when they hold the approve action. An agent never approves (ADR 128 decision 10 stays). ADR 193 decisions 4 and 9 read "eligible approver" with this meaning.

The sentences that the decision changes, for a job approval only: "An approver is a person in a session." and "A service principal, a job token and an access token never approve, also when they hold the approve action." The sentences "An author is eligible only when `authorMayApprove` is `true`" and "An agent never approves" are not changed for a person. For a machine, condition 2 is stricter than `authorMayApprove` (see "Self-approval").

Decision 7 holds, with no change, for `changeorder:approve` and `profile:approve`.

Changed-by chain (from `docs/adr/INDEX.md` line 194 and `rg` over `docs/adr` and `docs/rfc`):

- ADR 286 changes: the words catalog and promotion become self-service and failover. No effect on decision 7.
- ADR 299 changes decisions 1, 7 and 12: "`authorMayApprove` does not apply to a review request, mode 1 counts reviews on the Git host, and the binding level replaces signature carry-over." It gives no rule on the kind of principal that approves a job.
- ADR 385 changes decision 5. It does not name decision 7.
- No ADR changes decision 7 for the kind of the approver. Current rule: ADR 225, with ADR 286, 299 and 385.

Texts that cite decision 7 and that the ADR author must read again (an accepted ADR and an accepted or discussing RFC do not change, ADR 1 and `docs/rfc/README.md`): ADR 193 (status line: decisions 4 and 9 "read the eligible approver of ADR 225 decision 7"), ADR 222 (the table row for ADR 193 decisions 4 and 9), RFC 86 (lines 43 and 146), RFC 89 (line 119), RFC 91 (line 1205), RFC 93 (lines 629, 653, 701 and 702), RFC 102 (lines 186 and 900) and RFC 114 (lines 889 and 895). They cover change orders, profile drafts, review requests, packages and tickets. Each keeps decision 7 under this RFC, because the owner decision names a job approval only.

### ADR 385 decision 2

Verbatim, from `docs/adr/0385-a-fresh-step-is-opt-in.md` line 36, the last paragraph of decision 2:

> With the setting off, an access token that holds the grant may approve a `destroy` (owner, 2026-11-15). The handler, the service and the store each read the same effective value. None of them keeps a rule of its own.

The sentence that the decision changes is the first one. As written, any access token that holds the grant approves a `destroy`, and no rule names it. Owner decision 7 narrows this: a non-person principal approves only when the rule names it (condition 1), and never when the requirement sets a fresh window (condition 3). The second and third sentences stay.

ADR 385 decision 2 also lists the steps that "off means no step" covers, and it lists "an approval of a `destroy`, an approval in a namespace with `freshAuthForApprovals`, and an approval rule with `freshAuthSeconds`". Decision 3 says: "An access token never gives one." Both stay. A machine approval is an approval with no fresh step, and condition 3 keeps it away from each requirement that needs one (see "Fresh window").

Changed-by chain (from `docs/adr/INDEX.md` line 353): none. ADR 385 is the current rule alone. It is the newest ADR on the question, from 15 November 2026. Decision 7 of ADR 385 ("ADR 354 stays: an agent never has a fresh authentication") is not touched.

## Scope

| Principal kind | Credential | Job approval after the ADR |
| --- | --- | --- |
| Person | console session | as today (ADR 225 decisions 6 to 8) |
| Person | access token | only when a rule names the person for machine approval (condition 1) |
| Service principal (`KindService`) | access token | only when a rule names it (condition 1) |
| Workload (`KindWorkload`) | trust token | refused (no owner decision) |
| Any | job token | refused (no owner decision) |
| Agent (`KindAgent`) or any request with an agent reference | any | refused (ADR 103 decision 7, ADR 128 decision 10, ADR 299 decision 35, ADR 385 decision 7) |

The rejection of a job stays open to every holder of `job:approve`, as on main (the comment at `pkg/job/service_approve.go:251` says "any holder of job:approve can reject"). It is unchanged by this RFC. A rejection only stops a change.

## Options

1. **Do nothing.** ADR 225 decision 7 and ADR 385 decision 2 stay in conflict, and the code accepts any credential. Cost: a reader cannot tell which owner decision holds. This option conflicts with the owner decision.
2. **Name machine approvers in a separate list of the rule (recommended).** An approver entry list `machineIds` holds the stable principal IDs. `groups`, `users` and `unitOwners` never admit a non-person. Cost: one more field in `Approvers` (`pkg/gate/rules.go:147`), the gate, the stored job body and the API. Gain: a group or `job:approve` alone can never admit a machine, and the type of the field shows it (a class of bug that cannot happen, as the architecture rule asks).
3. **A flag on each user or group entry.** An entry gets `machine: true`. Cost: one entry with two meanings, and a later edit of the flag widens it. On a group entry the flag would name a set, not an ID, and condition 1 forbids that.

## Proposal

Option 2, with the words below for the future ADR. The ADR text is a proposal until the owner accepts it.

1. **Who is a machine approver.** A request whose credential is an access token (`CredentialAccessToken`; an empty credential counts as one, `pkg/identity/identity.go:75`) and whose kind is `KindService`, or whose kind is `KindPerson` (the access token of a person). A console session of a person is not a machine approver. A job token and a trust token are not machine approvers: they stay refused (`pkg/identity/identity.go:137` shows that `ActsAsPerson` is false for both). Note that `ActsAsPerson` is true for the access token of a person, so it cannot be the test of this rule. The test is the credential and the kind.
2. **The rule names it by stable ID (condition 1).** `Approvers` gets `MachineIDs`, a list of principal IDs (prefix `prn-`). The config service checks at save that each ID is a stored principal of kind `service`, or of kind `person`, and stores the ID. A name never counts. A principal that someone removes and makes again has a new ID, so it never inherits the entry (ADR 362 decision 1). An empty or unknown ID counts no one.
3. **A person in a session is not changed.** An entry in `users` or in a group admits a person in a session. It never admits the access token of that person. The access token of a person approves only when the person's ID is in `machineIds`. So the rule says who may approve with a token, and an operator sees it in the rule.
4. **Self-approval (condition 2).** See "Self-approval". The refusal ignores `AllowSelf` and `authorMayApprove`.
5. **Fresh window (condition 3).** See "Fresh window".
6. **Marks (condition 4).** See "Audit and decision record".
7. **The count.** A machine approval fills one slot of one requirement, as a person does (ADR 225 decision 6). The distinct-subject rule holds: one subject fills one slot.
8. **Scope.** Only `job:approve`. `changeorder:approve` and `profile:approve` keep ADR 225 decision 7.

### Self-approval

Condition 2 says "never approves a job that it started or authored". On main:

- The job holds `StartedBy`, the subject of the starter (`pkg/job/job.go:456`). `checkApprover` refuses the starter when no gate sets `AllowSelf` (`pkg/job/service_approve.go:225`), and `Gate.counts` does the same (`pkg/job/gate.go:266`). Both let the starter fill a slot when `AllowSelf` is true.
- The job has no field for the author of a plan that someone uploaded. I found none in `pkg/job/job.go`, `pkg/job/gate.go` or `pkg/job/service_approve.go`. [ADR 378](../adr/0378-the-binding-of-an-uploaded-plan.md) decision 4 says "The person who uploaded a plan never approves it." The code that holds that rule belongs to the upload change, and the machine rule must use the same record when it exists.
- A principal record has no creator or owner field (`pkg/principal/principal.go:180`). A token record has `CreatedBy`, the subject of the caller that made the token (`pkg/accesstoken/accesstoken.go:159`).

The proposed words:

- A machine approval is refused when `Approval.Subject` equals `StartedBy`, also when a gate sets `AllowSelf`. For a machine, `AllowSelf` never applies. The same holds in `GatesStatus`, so a stored approval that breaks the rule fills no slot (fail closed at the reconciler).
- A machine approval is refused when the subject is the author of the plan that the job applies, by the record that ADR 378 decision 4 needs.
- **Not covered by the words of the owner, and proposed here:** the machine approval is refused when `Token.CreatedBy` of the token that approves equals `StartedBy`. Without it, a person starts a job, makes a service token, and the service principal approves: one person fills two roles, and neither "started" nor "authored" names the service principal. This is the open question 1.

### Fresh window

Condition 3 says that a machine "cannot meet a requirement that sets a fresh window". A machine has no fresh step ([ADR 385](../adr/0385-a-fresh-step-is-opt-in.md) decision 3, "An access token never gives one"; ADR 354). Three things can ask for a fresh step of an approval:

- `freshAuthSeconds` of the rule or the default, set (none excluded; 0 and 60 to 900 count): ADR 225 decision 1, `pkg/gate/rules.go:77`.
- The setting `freshAuthForApprovals` of the namespace: ADR 225 decision 5.
- The operation `destroy`: ADR 225 decision 5 ("always"), and ADR 385 decision 2 for the setting off.

The proposed words: a requirement that stores `freshAuthSeconds` is out of reach for a machine, whatever the effective value of `freshAuthEnabled` is. The stored value is the fact. The setting only says if a person gets a step asked. Reason: ADR 385 decision 6 keeps the stored value with the mark `"enforced": false`, and an operator may turn the setting on later. A machine approval that counted while the setting was off would then stop counting with no change of the job. The other reading is open question 2. For `freshAuthForApprovals` and for `destroy`, the effective value of `freshAuthEnabled` decides (ADR 385 decisions 1 and 2): with the setting on, a machine cannot approve (it cannot give the step), and with the setting off, a named machine may approve a `destroy`. This is the narrowed form of the sentence of ADR 385 decision 2.

In terms of the matching of the change `several-approval-requirements-one-change` (Part A): `For` of a machine approval holds only the gates with no window. When no gate is left, the approval is refused (the refusal of task 5.4 for a stale step, with the cause "no fresh step").

### Audit and decision record

The proposed words, so that a reader sees a machine approval in each place that records an approval:

- **Audit entry.** `approvalEntry` (`pkg/job/service_approve.go:391`) writes the change `machine` with the value `true`, and the change `credential` with the value `access-token`. It writes the entries that exist already: `TokenID` of the principal (`Entry.TokenID`) and `PrincipalKind` (`pkg/audit/audit.go:437`). A person's access token has the kind `person`, so `PrincipalKind` alone does not tell a token from a session. The change `machine` does. A refusal of a machine approval writes an entry of the outcome `denied` with the cause, so that a hostile try is visible.
- **Stored approval.** `Approval` (`pkg/job/gate.go:172`) gets `Machine bool` and `Credential`, and the job body codec keeps them. They are not set by the client.
- **Decision record.** `Signature` (`pkg/gate/record.go:91`) gets `Machine bool` and `TokenID`. The console and the jobs API show "machine approval" next to the subject.
- The record also names the entry of `machineIds` that admitted the principal.

## Effects on the OpenSpec change `several-approval-requirements-one-change`

Read at `openspec/changes/several-approval-requirements-one-change/` on main (version 8 of the proposal). The ADR check of its proposal quotes main at `65b8436656e6...`. This RFC checked the cites against main at `74db479f9e34...`. The tasks that this RFC and the later ADR affect:

| Task or text | Effect |
| --- | --- |
| Task 5.6 (kind of the approver, `checkApprover`, `ActsAsPerson`, the credential kind, the `destroy` approval with an access token when `freshAuthEnabled` is off) | Unblocked by the ADR, not by this RFC. It implements the words above: `MachineIDs`, the self-approval refusal for a machine, the no-window rule, the marks. The sentence "Do not start before the answer is a repository record" holds until the owner accepts the ADR. |
| Task 5.1 (users by stable ID: `UserIDs` on `Approvers`, `Gate`, the port `PrincipalIDs`, `gatesOf` builds the gate) | Gets a sibling: `MachineIDs` on `Approvers` and on `Gate`, the same save check, and the same "no ID counts no one" row. Best done in task 5.1 or in task 5.6 with one codec change. |
| Task 5.4 (a fresh window for each requirement; `For` only when the fresh step meets the window) | A machine approval has no step. `For` of a machine holds only the gates with no stored window. Task 5.4 and 5.6 touch the same lines of `checkApproval` (`pkg/job/service_approve.go:83`) and must land in an order that the PM names. |
| Task 5.7 (the repository records after each answer) | Names a "later answer to the open question" record. This RFC and the ADR are that record. The week log and the queue get the entry the same day. |
| Task 2.3 (`checkApprover` applies the self-approval rule by gate) | The machine refusal ignores `AllowSelf`. Task 2.3 must not fold the machine rule into the `AllowSelf` check at `pkg/job/service_approve.go:225`. |
| Task 1.1 (`GatesStatus` test table) and task 1.2 (rows of `service_approve_groups_test.go`) | Part A rows stay. The machine rows belong to task 5.6. |
| Tasks 3.1 and 3.2 (hostile tests and "Defense in depth" comments) | The layer table of the design gets the rows of "Hostile cases" below. |
| Proposal, "Out of scope" first bullet ("Who may approve by kind of principal ... This is the open question") and "Open questions: 1, parked" | Change to "decided by the owner (2026-11-20), ADR <number>" after the ADR. The park lifts for task 5.6 only. |
| Proposal, ADR check, the entries for ADR 225 and ADR 385 ("Conflict, open") | Change to the new ADR and the chains. |
| Design, decision 15 ("The kind of the approver is not changed here") and the first item of "Risks" (line 163 region) | Replace with the words of the ADR. |
| Spec `specs/approval-requirements/spec.md`, the requirement on approval by a person (line 125: "When a person approves, the server SHALL check the person against each gate") | A delta adds a requirement for the machine approver with the scenarios of "Hostile cases". |
| Docs: `docs/guides/approval-rules.md` | One section on `machineIds`: who may approve with a token, the four conditions, and the one command to list the entries. |

Part A (the matching of several requirements) does not depend on this RFC.

## Security analysis

Each guard needs its own test at each layer, as ADR 250 asks (`CLAUDE.md`, "Security code"). The layers for a job approval are the rule save (config service), the store codec, the service (`Approve`), the matcher (`GatesStatus`) and the HTTP handler.

### Self-approval

Four paths exist for one person to fill two roles with a machine:

1. The person starts the job and approves with an access token of their own. The subject is the same, so the existing refusal holds (`StartedBy`). It must hold for a machine even when `AllowSelf` is true.
2. The person starts the job and a service principal that the person made approves. The subjects differ. Condition 2 as the owner worded it does not stop this. The proposal in "Self-approval" (`Token.CreatedBy`) stops the token path. A service principal with a second token that another person made is not stopped, and no field on main links a principal to a creator.
3. The person uploads a plan and a machine approves. ADR 378 decision 4 covers a person. The machine rule must read the same record.
4. A job that a trigger or a schedule starts: `StartedBy` names the starter that the trigger records. If the machine is that starter, the first rule holds. If the machine only owns the rule that names it, nothing links it to the job.

### Fresh window

A machine never gives a fresh step, so the approval check must treat it as "no step" at each requirement that asks for one. The risk is a path that fills a slot of a requirement with a window by a machine approval. The matcher must hold the rule too: `GatesStatus` computes from the stored `For`, and a stored approval with a machine mark and a gate that has a window fills no slot, even when the store holds it (a restored backup, or an older code version).

### Impersonation by the access token of a person

An access token is a bearer secret. Anyone who holds it acts as the person, with no MFA. The proposal keeps this risk small in three ways:

- A rule must name the person in `machineIds`. Each person in a `users` entry or a group stays a person in a session only.
- The audit entry records `TokenID` and `machine`, so a review sees which token approved.
- The owner can end the path at once: remove the ID from the rule, or revoke the token.

The residual risk stays: a stolen token of a named person approves a change. Four-eyes then holds only as far as the token holders are different people. The owner accepted this for a named principal in decision 7. A rule can reduce it with a count of 2 or more and a second approver that is a person (open question 3).

### The kinds that stay refused

A job token is the credential of the job itself. If it could approve, a job would approve its own change. A trust token of a workload has no person and no owner link. Both stay refused with `ErrAgentDenied` or `identity.ErrDenied`, and the refusal is audited. An agent keeps `ErrAgentDenied` for each verdict that approves, also when it acts with the access token of a person: `Actor.Agent` is checked before the credential (`pkg/job/service_approve.go:209`).

### Audit

Each approval and each refusal of a machine writes an entry in the same transaction as the verdict (`Store.Apply` with `[]audit.Entry`, `pkg/job/service_approve.go:347`). The entry holds the rule entry that admitted the principal. A refusal writes the cause. No silent fallback exists: a machine that no rule names gets a refusal, not a person-level check.

## Hostile cases

Each case needs a test per layer that fails when the guard is removed. "Layer" lists where the guard sits.

| # | Case | Expected | Layer |
| --- | --- | --- | --- |
| 1 | A service principal holds `job:approve` and is in no rule | refused; audited | service, handler |
| 2 | A service principal is a member of a group that the rule names | refused; a group never admits a machine | service, matcher |
| 3 | The rule names a person in `users`, and the person approves with an access token | refused; only `machineIds` admits a token | service, matcher |
| 4 | The rule names the service principal by name, not by ID | refused at save (`gate.ErrInvalid`) | rule save |
| 5 | The service principal is removed and made again with the same name | the new principal fills no slot; the rule holds the old ID | service, store |
| 6 | A machine starts a job and then approves it, with `AllowSelf` true | refused; `AllowSelf` does not apply | service, matcher |
| 7 | A person starts a job, makes a token for a named service principal, and the principal approves | refused (the proposed `Token.CreatedBy` rule); open question 1 | service |
| 8 | The requirement stores `freshAuthSeconds` 300 and `freshAuthEnabled` is off | a machine fills no slot (proposed reading); open question 2 | service, matcher |
| 9 | A `destroy` approval, `freshAuthEnabled` on, a named machine | refused with `mfa.fresh-step-required` | service, handler |
| 10 | A `destroy` approval, `freshAuthEnabled` off, a named machine, no window stored | accepted; the entry marks `machine` | service |
| 11 | The namespace sets `freshAuthForApprovals`, `freshAuthEnabled` on, a named machine | refused | service |
| 12 | Two gates: A has a window, B has none; a named machine approves | `For` holds B only; A stays open | service, matcher |
| 13 | A job token approves | refused; the job never approves its own change | service, handler |
| 14 | A trust token of a workload approves | refused | service |
| 15 | An agent acts with the access token of a named person | refused with `ErrAgentDenied` | service |
| 16 | A stored approval with `Machine` true and a gate with a window, from a restored backup | fills no slot | matcher, store |
| 17 | A client sends `machine: false` or a `For` list in the body of the approval | ignored; the server sets both | handler |
| 18 | The machine approves twice | the second call is `job.already-approved`; one slot | service |
| 19 | `count` is 2 and two named machines approve | allowed by the decision; open question 3 | matcher |
| 20 | A rejection by a service principal that no rule names | the job is rejected (as on main) | service |
| 21 | The audit write fails | no verdict is stored | store |
| 22 | A machine approves a change order or a profile draft | refused (ADR 225 decision 7 stays) | service |

## The words of the future ADR

The ADR is not in this pull request. These are the items that it needs, in the form of ADR 296 (decisions 2 to 6).

The "Changes:" list of the new ADR:

- Changes ADR 225 decision 7: a service principal, or the access token of a person, may approve a job when the approval rule names it by stable ID, under four conditions. The sentences "An approver is a person in a session." and "A service principal, a job token and an access token never approve, also when they hold the approve action." hold for a change order and a profile draft, and for a job token and a workload.
- Changes ADR 385 decision 2: the sentence "With the setting off, an access token that holds the grant may approve a `destroy`" holds only for an access token that the rule names, and only for a requirement with no stored fresh window.
- Extends ADR 225 decision 6: a machine approval fills one slot, and one subject fills one slot.
- Extends ADR 362 decision 1: a rule names a machine approver by stable ID.
- Extends ADR 227: a machine approval has no fresh step.

The "Changed by:" items that the same pull request adds:

- On ADR 225: "- ADR NNN changes decision 7: a machine approver of a job, named by ID in the rule."
- On ADR 385: "- ADR NNN changes decision 2: an access token approves a `destroy` only when the rule names it."
- On ADR 227 and ADR 362: one "extends" item each.

Then `make adr-index` and `make docs-lint`. Because ADR 296 decision 6 is the only change that an accepted ADR gets, no other edit is made in ADR 225 or ADR 385.

Decision text, in the form that ADR 14 asks (strict STE for the ADR itself, to be checked by `make docs-lint`):

1. A service principal, or the access token of a person, may approve a job. The approval rule names it by stable ID in `machineIds`. A group, `users`, `unitOwners` and the action `job:approve` do not name it. (owner, 2026-11-20)
2. A machine never approves a job that it started or authored. `AllowSelf` and `authorMayApprove` do not apply to it. (owner, 2026-11-20)
3. A machine never fills a requirement that sets a fresh window. (owner, 2026-11-20)
4. The audit entry and the decision record mark each machine approval. (owner, 2026-11-20)
5. A job token, a trust token and an agent never approve. A change order and a profile draft keep ADR 225 decision 7. (PM)
6. The server refuses a machine approval when the token was made by the starter of the job. (PM, open question 1)

## Open questions

1. **Token created by the starter.** Condition 2 names "started or authored". A service token that the starter made is a path around it (hostile case 7). The proposal adds a refusal by `Token.CreatedBy`. Does the owner want it? It cannot cover a principal that a person made and a second person runs, because no principal field links a creator. Recommendation: yes, and file a bug issue for a creator field on the principal record, as a separate change.
2. **A stored window with the setting off.** Does "sets a fresh window" mean the stored `freshAuthSeconds`, or the enforced one? Reading A (proposed): the stored value, so the machine never fills the requirement, and a later turn-on of `freshAuthEnabled` changes no job. Reading B: the enforced value, so a machine fills a requirement with a stored window while the setting is off. Cost of B: the same job changes meaning when the setting changes. Recommendation: A.
3. **At least one person.** Two named machines may fill a count of 2, and no person looks at the change. The decision does not forbid it. A rule field `minPersons` would. Recommendation: do not add it now. A rule that wants a person names a group or `users` for one requirement and `machineIds` for no requirement. The ADR says that machine approval is the choice of the rule author.
4. **Name a token or a principal.** The proposal names the principal ID, so a rotated token keeps working. Naming the token ID would limit the entry to one token, at the cost of a rule edit at each rotation. Recommendation: the principal ID, as the owner says "stable ID", and the audit entry names the token.
5. **Where the field lives.** The rule field `machineIds` and the gate field need an API change (`approvers` of an approval rule and an approval default, the console form). The tool-neutral APIs may break before 1.0 (ADR 73), and the approval API has no compatibility need.
6. **The `destroy` approval and ADR 385 decision 2.** The owner decision of 15 November says an access token approves a `destroy` with the setting off. Under owner decision 7 it does so only when named. Is this the owner's intent for the earlier sentence, or must the earlier rule (any token that holds the grant) stay for a `destroy`? The words of decision 7 say "A group or `job:approve` alone does not count", and the PM reads this as the narrowing. Recommendation: the narrowing, and the ADR says so.

## Checklist

- [x] Tolerates asynchronous replication between installations: a job approval is one write of one namespace, under its fence, with its audit entry in the same transaction. This RFC adds no write path, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
