Roadmap: `docs/roadmap.md:782` (A revoke stops the work).

## Why

**Persona: developer.** A developer delegates work to Dusty and later must stop it. The developer revokes the delegation, checks that the work of the delegation stopped, and reads the audit entries that prove that the next request was refused.

The user path this change must make work:

1. **API.** Revoke the delegation with the route of `person-delegates-dusty`. The answer shows the end time and the cause `revoked`.
2. **API.** Read the work of the delegation: a queued job is canceled, a trigger or a schedule is disabled, and each carries the stop reason `delegation-ended`.
3. **API.** Send one more agent request under the delegation and read the refusal.
4. **Docs page.** Read the revoke section of the audit log guide and run the verify command as written.
5. **Console.** Read the end entry and each refusal entry with the agent, the delegation and the cause. The console pages are in the console repository.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A request of the agent under an ended delegation | 403 with the NEW error code `delegation.ended`. The text names the delegation ID and the cause of the end, and no secret. | `GET /api/audit/entries` |
| The store cannot resolve the delegation | The request is refused. During a database gap the answer is 503 with `Retry-After: 5` (ADR 298 decision 2). | `GET /api/audit/check` |

Evidence for the claims above:

- No stop reason `delegation-ended` exists on main. Proof: `rg -n 'delegation-ended' -g '*.go' .` prints nothing.
- No code on main resolves a delegation, because no delegation exists. Proof: `rg -n 'type Delegation(Record)? ' -g '*.go' .` prints nothing. The record and `Resolve` are in the change `person-delegates-dusty`.
- The audit log has read and export routes and guides on main. Proof: `internal/api/audit/routes.go:49` (`/api/audit/entries`), `e2e/audit_export_test.go` and `docs/guides/audit-log.md`.
- The stop reasons of an agent run are not in code on main. Proof: `rg -n 'credential-ended' -g '*.go' .` prints nothing. The run loop of Dusty has ports and no adapter yet (`docs/roadmap.md`, "Accepted designs that wait for adapters").

## What Changes

- Every use of a delegation resolves it again, through `Resolve` of `person-delegates-dusty`, and refuses the request with the NEW error code `delegation.ended` when the delegation has ended (RFC 121 decisions 16 and 31). The server holds no cache of an allow verdict.
- A job that waits, or that has not started a phase that changes, is canceled with the stop reason `delegation-ended` (RFC 121 decision 33).
- A running phase gets the refusal at its next call with the job token, with the rule of ADR 354 decision 2: a phase that the person confirmed finishes, no new phase starts, and every phase that the person did not confirm stops at its next call. An installation setting holds this behavior, with that behavior as the default. The API shows the effective value (open question 1 asks for the shape).
- A run trigger, a drift schedule and a scheduled change of the delegation are disabled with the stop reason `delegation-ended`. A job that a trigger would start from a write of an ended delegation does not start (RFC 121 decisions 33 and 34).
- A Dusty run that names an ended delegation ends at its next step or before its next tool call with the stop reason `delegation-ended` (RFC 121 decision 9: the run resolves the delegation "at each step and before each tool call").
- Each refusal writes one audit entry that names the agent, the delegation and the cause (RFC 121 decision 51). The shape of the entry is the one of `every-step-audited`.
- An agent never revokes a delegation: the action has the agent class `never` (RFC 121 decision 20 (e)). A revoke request of an agent is refused and written as a denial.
- Add the stop reason `delegation-ended` to the GLOSSARY entry "Stop reason" (done in this pull request).

Out of scope:

- The delegation record, the create, list and revoke routes, the end causes and the audit entries of the ends. They are in `person-delegates-dusty`.
- The agent audit entry shape and the chain check. They are in `every-step-audited`.
- Drafting and confirming a change order. They are in `dusty-proposes-change-order` and `person-confirms`.
- The held requests of RFC 121 decision 23, and the rule "A confirmed, unused held request of an ended delegation is never used". They are RFC 121 slice 4.
- The suspension of a delegation after a burst of refusals (RFC 121 decisions 5 and 19).
- The notices before the expiry (RFC 121 decision 15).

Depends on:

- `person-delegates-dusty` (the record, `Resolve` and the end causes).
- `every-step-audited` (the shape of the refusal entry).
- A change that makes a job, a trigger, a schedule and a Dusty run carry the delegation (RFC 121 decisions 9 and 31, slices 3 and 6). No such change is in `openspec/changes/` on 20 November 2026. It is a dependency to be written. The changes `give-dusty-tools-comment-propose` and `benchmark-dusty-runs-wait-approval` are not that change.

## Usability

Where the path still costs the person time or surprises them:

- The revoke answer says which delegation ended and the cause, and the work list shows what stopped and what finished. A confirmed phase that finishes shows as such, so the person does not read it as a failure of the revoke.
- A refused agent call names the cause of the end and the one command that lists the delegation.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Revoke checks the object before it revokes. (`pkg/access/access_test.go:1163 (TestRevokeChecksTheObject)`)
- Grants on a principal take act-as actions only in a pin test. (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)
- The audit hash chain walks from a start sequence. (`pkg/audit/chain_test.go:259 (TestChainWalkStart)`)
- The audit entries have a read route and a check route. (`internal/api/audit/routes.go:49`)

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged: "Decided by the project owner on 7 November 2026").

- Decisions that apply: 2 and 6 (current). Changed by: -. Chain to the end: none. Current rule: this ADR.
- Quote, decision 2: "A phase that the person confirmed finishes, because the person saw and allowed that exact plan. No new phase starts. Every phase that was not confirmed stops at its next call. This is configurable: an installation setting holds the behavior, with the recommended value as the default. The API shows the effective value."
- Effect: the stop of running work follows decision 2. The roadmap text "all running work of the delegation stops" differs from it for a confirmed phase (open question 5).
- Conflict: none with the ADR. See open question 5 for the roadmap.

**RFC 121, accepted by ADR 354 decision 6 (owner, 2026-11-07).**

- Decision 16: "A delegation and its work stop at the next resolve when any of these happens: the person revokes it, it expires, the agent is disabled or deleted, or the person is disabled, deleted, removed by SCIM or terminated by the identity provider".
- Decision 33: "a job that waits, or has not started a phase that changes, is canceled with the stop reason `delegation-ended`" and "a trigger or a schedule is disabled with the same reason".
- Decision 31: "At each later evaluation (`change.planned`, `change.apply`, `state.write` and each call with the job token), the server resolves the delegation and runs both parts."
- Decision 20 (e): an action that "creates, renews, widens or revokes a delegation" has the agent class `never`.
- Decision 51: "Each delegation create, renew, use, revoke, expiry and end ... writes an entry."
- Effect: the stop reason is `delegation-ended`, not `revoked`. The refusal is at the next resolve. The agent cannot revoke.
- Conflict: none.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 14 (changed by ADR 354) and 19 (current). Changed by: ADR 128, 129, 210, 297, 311, 314, 354, 367. Chain to the end: ADR 128, 129, 210, 297, 311, 314, 354, 367, 190, 222, 227, 286, 247, 301, 333, 357, 225, 299, 300, 344, 365, 335, 351, 320, 339, 373, 385, 372, 328, 359, 368, 377, 332, 376. Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354, 367.
- Quote, decision 19: "The holder of the turn gets the cancel at its next write. Its step context ends, so a model call in flight stops."
- Quote, decision 19: "A cancel never undoes a change that a job applied."
- Effect: a run of an ended delegation ends at its next write boundary, as a cancel does. A change that a job applied stands.
- Conflict: none.

**ADR 385: A fresh step is opt-in** (owner-tagged) and **ADR 227: One fresh-authentication window** (decisions 2 and 3 are owner-tagged).

- ADR 385: current rule this ADR; changed by -. ADR 227: current rule this ADR, with ADR 301, 320, 339, 354, 373, 385.
- Quote, ADR 385 decision 7: "ADR 354 stays: an agent never has a fresh authentication, and an agent holds no right that a person has."
- Effect: whether a revoke needs a fresh step is open question 2. No ADR line names a revoke.
- Conflict: none known.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current). Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337. Current rule: this ADR, with ADR 337.
- Quote, decision 2: "Requests fail fast with 503."
- Effect: a refusal that needs the database during a gap answers 503.
- Conflict: none.

## Open questions

Open questions: 5, parked

The questions below are not decided. No ADR line and no RFC line settles them. The owner decides each one. This change is not spec-ready until each one has an answer in a repository record.

1. **What is the shape and the name of the setting of ADR 354 decision 2?** The ADR says "an installation setting holds the behavior" and gives the recommended behavior as the default. Options:
   - (a) A boolean `agent.finishConfirmedPhase`, default true. Cost: two behaviors only. A third behavior, such as "stop at once, also a confirmed phase", is the value false and nothing else.
   - (b) An enum with the values `finish-confirmed` and `stop-now`, default `finish-confirmed`. Cost: one more word to document. A later third value does not break a stored value.
   - (c) A part of the installation agent policy of RFC 121 decision 19 when its slice lands. Cost: this change has no setting until then, and the behavior is fixed in code, which ADR 354 decision 2 forbids.
   - Decides: the owner.
2. **Does a revoke need a fresh step?** RFC 121 decision 14 names create, renew and widen. Decision 17 names list and revoke and no step. ADR 385 decision 2 does not list a revoke. Options:
   - (a) No step on revoke, in any setting. Cost: a stolen console session can revoke a delegation, which stops work and makes no new right. The cut is the fast path in an emergency.
   - (b) A step when `freshAuthEnabled` is on. Cost: a person with no factor, with the setting on, cannot revoke their own delegation (ADR 301 decision 4: "No factor, no fresh step"). An administrator with `server:manage-access` can.
   - (c) A step always. Cost: the same as (b) with the setting off, and it contradicts "off means no step" (ADR 385 decision 2).
   - Decides: the owner. The old provisional answer of this change was (c). It is withdrawn.
3. **Does an ended delegation end idle and queued work at once, or at the next resolve?** RFC 121 decision 16 says "at the next resolve". A queued job that no claim picks up, a trigger that does not fire and an idle run never resolve. Options:
   - (a) Only the next resolve. Cost: a queued job stays "queued" in the list until a claim reads it, so the list may show a job that will be canceled.
   - (b) The revoke transaction cancels the queued work of the delegation, and a sweep covers the other ends. Cost: a write of many rows inside the revoke, and a sweep with a fence, as open question 5 of `person-delegates-dusty` says.
   - Decides: the owner.
4. **Does each refusal write its own entry when a trigger refuses again and again?** RFC 121 decision 51 says that each refusal writes an entry. A schedule that fires every minute writes 1440 entries a day for one ended delegation. Options:
   - (a) One entry for each refusal. Cost: the log grows with the schedule, until the trigger is disabled. Decision 33 disables a trigger or a schedule at the end, so the number is one for each trigger after the first refusal.
   - (b) One entry for each delegation and cause in a period that a setting holds. Cost: one more setting, and the chain does not show each refusal.
   - Decides: the owner.
5. **The roadmap step 5 differs from ADR 354 decision 2.** The step says: "all running work of the delegation stops". ADR 354 decision 2 says that a confirmed phase finishes by default. The ADR "Consequences" changed step 3 of the exit test and did not name step 5. Options:
   - (a) Change the text of step 5 in `docs/roadmap.md` to name the confirmed phase and the setting. Cost: a roadmap edit that the owner approves.
   - (b) Keep the text and make the default of the setting `stop-now`. Cost: this contradicts the recommended default that ADR 354 decision 2 gives.
   - Decides: the owner. This change does not edit `docs/roadmap.md`.

Questions considered and answered:

- May an agent revoke its own delegation? No. RFC 121 decision 20 (e).
- Does a revoke undo a finished tool call? No. ADR 120 decision 19: "A cancel never undoes a change that a job applied."
- What happens to a draft change order of the ended delegation? The change `dusty-proposes-change-order` owns it (its open question 3).
- What is the stop reason? `delegation-ended` (RFC 121 decision 33). The earlier draft of this change used `revoked`. That word is only the cause of an end.
- Does a sweeper with a 30 second period and a fenced lease belong to this change? No line asks for it. Open question 3 asks whether any sweep exists. The earlier draft named the numbers without a source, and they are removed.
- What if the store cannot resolve the delegation? The request is refused (fail closed). During a database gap the answer is 503 (ADR 298 decision 2).
- Who may revoke, and what happens on unknown ID, repeat and restore? `person-delegates-dusty` owns it. A delegation has no restore: a new delegation has a new ID.
