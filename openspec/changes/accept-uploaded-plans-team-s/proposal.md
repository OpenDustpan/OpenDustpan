---
version: 7
---

Issue: #915.

## Why

**Persona: developer.** A developer runs plans and applies in the own CI of the team. The developer uploads the plan for review and approval and needs the apply to prove it is the approved plan.

The user path this change must make work:

1. **API.** Developer runs the plan in team CI and uploads plan-display and plan-apply parts through the API.
2. **Console.** Developer opens the change order, sees the uploaded-plan mark, and requests approvals under the approval rules.
3. **CLI.** Developer applies through the local wrapper, which presents the plan hash when it opens the apply claim (ADR 378 decision 3); the claim opens only for the hash of the approved plan.
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
- Apply-time check of the plan hash when the apply opens its apply claim through the local wrapper (ADR 378 decision 3), and a stale plan after any state write since the upload (ADR 378 decision 2).
- Docs page for uploaded plans.

Out of scope:

- Running the plan on runners.
- Pre-merge apply and apply after merge behavior.
- New approval rule kinds.

Depends on:

- none

Builds on (not a change):

- #899
- #901

## Usability

Where the path still costs the person time or surprises them:

- The docs page must state that the server did not run the uploaded plan.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- e2e drives the real OpenTofu CLI through a helper. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 307: OpenTofu only, uploaded plans, TFE run fields and run triggers** (owner-tagged: all decisions).

- Decisions that apply: 2 (current).
- Changed by: none. Current rule: this ADR.
- Quote: "**Own-CI teams may upload plans (owner, 2026-10-18).** The owner said: "Own-CI teams: accept uploaded plans". The owner said also: "the apply must present the same plan hash". The owner said also: "weaker than a runner plan, and marked so in the change order"."
- Effect: the change accepts an uploaded plan, the apply presents the same plan hash, and the change order marks the plan as weaker than a runner plan.
- Conflict: none.

**ADR 102: Job plans and logs: parts, chunks, uploads and reads** (not owner-tagged).

- Decisions that apply: 2 (changed), 7 (changed).
- Changed by: ADR 190 changes decisions 2, 7 and 8; ADR 194 extends decision 2; ADR 210 changes; ADR 220 changes decisions 3 and 4; ADR 299 changes decision 8; ADR 307 extends; ADR 353 extends decision 7. Chain to the end: ADR 190, ADR 194, ADR 210, ADR 220, ADR 299, ADR 307, ADR 353, ADR 222, ADR 225, ADR 300, ADR 344, ADR 365, ADR 226, ADR 247, ADR 286, ADR 273, ADR 363, ADR 328, ADR 359, ADR 368, ADR 335, ADR 351, ADR 354, ADR 240, ADR 241, ADR 258, ADR 263, ADR 342, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 190, 194, 210, 220, 299, 307, 353.
- Quote: "One plan for each plan phase, with two parts."
- Quote: "A plan binds approvals, and a stale plan never applies."
- Effect: The upload stores the two parts of decision 2 and binds approvals to both hashes as decision 7 does for runner plans. The uploaded case has no plan phase, so a new ADR must set its state version.
- Conflict: none.

**ADR 190: Change orders: an object above jobs** (not owner-tagged).

- Decisions that apply: 9 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359, ADR 368.
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

Questions considered and answered:

- What ADR text binds an uploaded plan to both part hashes and to a state version, and how is a state write after the upload detected? ADR 378 decision 1: "An uploaded plan binds to both part hashes and to the state version at upload (owner, 2026-11-14)." ADR 378 decision 2: "A state write after the upload makes the plan stale (owner, 2026-11-14)." An apply of a stale plan fails with 409.
- How does the apply present the plan hash? ADR 378 decision 3: "The apply presents the hash through its apply claim (owner, 2026-11-14)." The apply presents the hash when it opens its apply claim through the local wrapper, with the launcher token of ADR 369 decision 1. The hash is not in the tool request. The server refuses to open the claim when the hash is not the hash of the approved plan.
- Does self-approval apply to uploaded plans, and are destroy plans accepted? ADR 378 decision 4: "No self-approval and no destroy plan for an upload (owner, 2026-11-14)." The person who uploaded a plan never approves it. The server refuses an uploaded destroy plan.
- Does the hash binding need a new ADR? Yes. The issue says it needs an ADR, and ADR 102 decision 7 covers runner plans only. ADR 378 is that ADR.
- Is the hash match a gate? No. ADR 222 decision 4 keeps plan checks as fixed controls.
- Is the hash match a gate? No. ADR 222 decision 4 keeps plan checks as fixed controls.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
