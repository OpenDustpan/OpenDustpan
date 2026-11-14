---
version: 2
---

Issue: #915.

## Why

**Persona: developer.** A developer runs plans and applies in the own CI of the team. The developer uploads the plan for review and approval and needs the apply to prove it is the approved plan.

The user path this change must make work:

1. **API.** Developer runs the plan in team CI and uploads plan-display and plan-apply parts through the API.
2. **Console.** Developer opens the change order, sees the uploaded-plan mark, and requests approvals under the approval rules.
3. **CLI.** Developer applies with the same plan hashes; the state write or update start passes the hash check.
4. **Docs page.** Developer reads the uploaded plans guide for the upload call and the refusal text.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Upload without the plan grant is refused. | 403 unit:run-plan needed on the unit. Ask a namespace administrator for the grant. | `opendustpan status` |
| Apply with a different plan hash is refused and names the change order. | 409 plan changed: the approved plan of change order <co-id> has other hashes. Upload the current plan. | `opendustpan status` |
| Upload with a part over 256 MiB is refused. | 413 plan part too large: at most 256 MiB per part. | `opendustpan status` |

Evidence for the claims above:

- Issue 915 asks for a plan upload route, approval rules, an apply-time hash check, and a weaker mark. Proof: Scope - In: a plan upload route that makes a change order; approval rules; an apply-time check of the plan hash on the state write (OpenTofu) or the update start (Pulumi CLI); the "weaker" mark in the change order.
- Issue 915 excludes running the plan on our runners. Proof: Out: running the plan on our runners.
- Issue 915 says the hash binding needs an ADR. Proof: RFC 29 accepted; ADR 102 covers plans. Needs ADR for the hash binding.

## What Changes

- New route to upload both plan parts for one unit that makes a marked change order.
- Uploaded fields on the change-order read: the mark and both hashes.
- Apply-time check of both plan hashes at the OpenTofu state write and the Pulumi update start.
- Docs page for uploaded plans.

Out of scope:

- Running the plan on runners.
- Pre-merge apply and apply after merge behavior.
- New approval rule kinds.

Depends on:

- #899
- #901
- `mirror-approvals-between-review-request` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The docs page must state that the server did not run the uploaded plan.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- e2e drives the real OpenTofu CLI through a helper. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 102: Job plans and logs: parts, chunks, uploads and reads** (not owner-tagged).

- Decisions that apply: 2 (changed), 7 (changed).
- Changed by: ADR 190 changes decisions 2, 7 and 8; ADR 194 extends decision 2; ADR 210 changes; ADR 220 changes decisions 3 and 4; ADR 299 changes decision 8; ADR 307 extends; ADR 353 extends decision 7. Chain to the end: ADR 190, ADR 194, ADR 210, ADR 220, ADR 299, ADR 307, ADR 353, ADR 222, ADR 225, ADR 300, ADR 344, ADR 365, ADR 226, ADR 247, ADR 286, ADR 273, ADR 363, ADR 328, ADR 359, ADR 335, ADR 351, ADR 354, ADR 240, ADR 241, ADR 258, ADR 263, ADR 342, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 190, 194, 210, 220, 299, 307, 353.
- Quote: "One plan for each plan phase, with two parts."
- Quote: "A plan binds approvals, and a stale plan never applies."
- Effect: The upload stores the two parts of decision 2 and binds approvals to both hashes as decision 7 does for runner plans. The uploaded case has no plan phase, so a new ADR must set its state version.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (not owner-tagged).

- Decisions that apply: 9 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "An apply job applies the plan of its plan job."
- Effect: The apply names the stored plan; the uploaded plan has no runner plan job, which needs the new ADR.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: A docs page ships with the change.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Fixed controls stay fixed (owner)."
- Effect: The hash match is a fixed control, not a gate.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What ADR text binds an uploaded plan to both part hashes and to a state version that the server sets at upload, and how is a state write between the CI plan and the upload detected?
   - Options: New ADR for the uploaded hash binding with the server-set state version at upload; Reuse ADR 102 decision 7 without a new ADR.
   - Provisional decision (owner review 14 Nov): New ADR for the uploaded hash binding with the server-set state version at upload
   - Reason: An uploaded plan has no plan phase, so the version source needs an owner rule. Benchmark: AWS: change sets bind execution to a reviewed change set ID.
   - Persona impact: Developer waits for the ADR; no workflow breaks because the route does not exist yet. Escape hatch: No upload route ships before the owner accepts the ADR.
   - Owner-tagged ADRs involved: ADR 190 (their words do not change).
2. How does the apply present the plan hashes on the OpenTofu state write and the Pulumi update start, and how does the server map the write to the approved unit plan?
   - Options: Hash in existing metadata of the write; New hash fields on the tool APIs; Out-of-band link to the apply.
   - Provisional decision (owner review 14 Nov): Hash in existing metadata of the write
   - Reason: The tool APIs are contracts that the real CLI checks, and it cannot send an unknown field. Benchmark: Google: a signed artifact is verified by digest at deploy time.
   - Persona impact: Developer uploads the same parts for both tools. Escape hatch: Ship the OpenTofu check first and hold the Pulumi check.
3. Does self-approval apply to uploaded plans, and are destroy plans accepted?
   - Options: Refuse self-approval and destroy uploads; Allow both with a second approver.
   - Provisional decision (owner review 14 Nov): Refuse self-approval and destroy uploads
   - Reason: The server did not run the plan, so trust is weaker. Benchmark: ISO 27001: segregation of duties.
   - Persona impact: A developer who uploads needs a second person; the error says so. Escape hatch: Refuse until the PM decides.
   - Owner-tagged ADRs involved: ADR 225 (their words do not change).

Questions considered and answered:

- Does the hash binding need a new ADR? Yes. The issue says it needs an ADR, and ADR 102 decision 7 covers runner plans only. It is an open question for the owner.
- Is the hash match a gate? No. ADR 222 decision 4 keeps plan checks as fixed controls.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
