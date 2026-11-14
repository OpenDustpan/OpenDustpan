Roadmap: `docs/roadmap.md:782` (A revoke stops the work).

## Why

**Persona: developer.** A Developer delegates work to Dusty and later must stop it at once. The Developer revokes the delegation, checks that running and scheduled work stopped, and reads the audit record that proves the next request was denied.

The user path this change must make work:

1. **API.** Revoke the delegation to Dusty through the route of person-delegates-dusty and confirm the revoke record with holder, scope, creator, and time.
2. **CLI.** Watch live runs end and scheduled starts refuse; send one more agent request and read the denial.
3. **Docs page.** Read the revoke page and run the verify command as written.
4. **Console.** Read the revoke entry and each denied entry with agent, delegation, and reason.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Agent request after revocation is denied | 403 with the NEW error code delegation.revoked. The text names the revoked delegation id and no secret. Read the audit entries to confirm the denied entry. | `GET /api/audit/entries` |

Evidence for the claims above:

- No e2e test covers Dusty on main. Proof: Keyword Dusty in the pack has e2e_count 0 and e2e [].
- The audit log has read and export routes and guides on main. Proof: Routes ANY /api/audit/entries and ANY /api/audit/export with guides docs/guides/audit-log.md and e2e/audit_export_test.go.

## What Changes

- Every later use resolves the delegation row again and denies with the NEW error code delegation.revoked when revoked time is set.
- Live runs of the delegation end with the NEW stop reason revoked and live durable claims cancel at the next write boundary.
- No scheduled or queued work of a revoked delegation starts.
- Each denial after revoke writes an audit entry in the shape of every-step-audited that names the agent, the delegation, and the reason.

Out of scope:

- Delegation create, list, revoke route, revoke action, revoked time storage, unknown id, repeat revoke, and no restore of person-delegates-dusty.
- Live and scheduled Dusty run work of dusty-proposes-change-order.
- Agent audit entry shape and hash-chain verification of every-step-audited.

Depends on:

- person-delegates-dusty
- dusty-proposes-change-order
- every-step-audited
- `person-delegates-dusty` (order of the milestone review)
- `every-step-audited` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Revocation must confirm which delegation was revoked and what stopped, or the person cannot tell live work from queued work.
- A denied agent call must name the revoke and the one command that lists the delegation.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks revocation against the object in the access service. (`pkg/access/access_test.go:1089 (TestRevokeChecksTheObject)`)
- Main restricts a grant on a principal to delegation actions only. (`pkg/access/delegation_pin_test.go:58 (TestGrantOnAPrincipalTakesDelegationActionsOnly)`)
- Main walks the audit hash chain from a start sequence. (`pkg/audit/chain_test.go:258 (TestChainWalkStart)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 14 (changed), 19 (current).
- Changed by: ADR 128 changes decision 22; ADR 129 changes decisions 3, 7 and 20; ADR 129 extends; ADR 210 changes; ADR 297 changes decisions 13 and 14; ADR 311 extends; ADR 314 extends; ADR 354 changes decisions 13, 14, 15 and 16. Chain to the end: ADR 128, ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 190, ADR 222, ADR 227, ADR 286, ADR 247, ADR 301, ADR 333, ADR 357, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354.
- Quote: "A cancel stops the run at the next write. The person of a run, or a holder of `namespace:manage-agent-runs`, cancels it with the cancel route. The server records the request on the run, and cancels the durable operation"
- Quote: "A run acts as its person, at the time of each call. A run records the subject of the person and the ID of the credential that started it: the console session or the access token. It never stores the credential, and never"
- Effect: Revocation reuses the cancel boundary of decision 19 and adds one NEW stop reason revoked alongside the credential stop reason of decision 14.
- Conflict: none.

**ADR 129: The operator sets every field of the model connection** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "No default model, no default provider, no preset. The server fills no field of a connection with a value for a provider. The defaults are empty values, the `userAgent` of the release (decision 12) and the `retry-after` h"
- Effect: The NEW stop reason revoked follows the stop-reason extension that ADR 129 adds to ADR 120. No model connection field changes.
- Conflict: none.

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The person who delegated may confirm (owner, 2026-11-07). Question 1. The person who delegated (Alice) may confirm a change that her own agent proposed, by default. This is the confirmation step of ADR 125 and ADR 128. Four-eyes comes from an approval rule that counts Alice as the author: with `authorMayApprove: false` on the namespace, a second person must approve. A one-person installation keeps working."
- Effect: Revocation keeps the delegation model of person-delegates-dusty: each later step resolves the delegation row again and stops when revoked time is set.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the revoke route need a fresh authentication?
   - Options: Require a fresh authentication for revoke; Allow revoke with any valid session or token.
   - Provisional decision (owner review 14 Nov): Require a fresh authentication for revoke
   - Reason: Revoke is a sensitive security cut like a credential revoke, so a fresh step matches account practice. Benchmark: Google requires a fresh sign-in for a sensitive security change and names revoke of a delegation as sensitive.
   - Persona impact: A fresh step costs the person one MFA check at revoke time and upsets no persona when the docs page states it. Escape hatch: A holder without a fresh step asks another holder with a fresh step to revoke.
   - Owner-tagged ADRs involved: ADR 227, ADR 301 (their words do not change).
2. May an agent token call the revoke route for its own delegation?
   - Options: An agent token never holds delegation:revoke; An agent token may hold delegation:revoke.
   - Provisional decision (owner review 14 Nov): An agent token never holds delegation:revoke
   - Reason: Self-revoke by the agent blurs the person-confirms boundary, so only the person or a revoke holder revokes. Benchmark: AWS denies a revoked principal from acting on its own revocation and requires the person or an administrator to revoke.
   - Persona impact: Denying the agent its own revoke upsets no persona because the person revokes in the console. Escape hatch: An agent that needs revocation asks the person through a held request.
   - Owner-tagged ADRs involved: ADR 354 (their words do not change).

Questions considered and answered:

- Does a revoked delegation end running Dusty work or only block new work? ADR 120 decision 19 answers it: a cancel stops the run at the next write. Revocation records cancel requests on live runs and ends each run with the NEW stop reason revoked, so no step writes after the boundary. A tool call that already finished before revocation stands; a change order proposed before revocation stays proposed and still needs a person confirmation.
- Does scheduled work of the delegation still start after revocation? The RFC excerpt in the pack answers it: the server resolves that delegation again at each later step, and when the person revokes it the work stops. No scheduled turn, plan job, or queued step of the delegation starts after revocation.
- What stops a race between revocation commit and a scheduler or claim takeover? The revoke commits the revoked time and the cancel requests on live runs in one transaction. A sweeper owned by the worker component retries any cancel that did not land every 30 seconds under a 30 second fenced lease claim, so only one sweeper acts. Each sweeper retry writes no new audit entry unless it lands a cancel. If a cancel never lands within 5 minutes the run stays denied at each write boundary and the next claim ends it with stop reason revoked.
- Who may revoke, and what happens on unknown, repeat, or restore? Person-delegates-dusty owns who may revoke, unknown and repeat revoke, and no restore. This change only reads the revoked time that that change stores and specifies the denial, the stop, and the block that follow it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
