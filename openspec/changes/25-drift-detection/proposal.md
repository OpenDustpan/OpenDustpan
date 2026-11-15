---
version: 2
---

## Why

**Persona: developer.** Developer runs infrastructure against OpenDustpan and needs to know when real resources drifted from state, what the last check found, and the one next step to refresh it.

The user path this change must make work:

1. **Docs page.** Developer opens the drift status of a stack or workspace from the docs page.
2. **API.** Developer reads the last drift result with GET /api/dustpan/v1/namespaces/{}/units/{}/drift.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A drift check starts while one already runs. | 409 with the error code `drift.start-conflict`. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift` |

Evidence for the claims above:

- Drift detection waits on runs. Proof: | [RFC 25](rfc/0025-drift-detection.md) Drift detection                                              | runs (milestone 5)                                              |
- Main defines a drift start conflict error code. Proof: internal/errcode/catalog_drift.go:8
- Main checks the drift shape in the API description test. Proof: pkg/dustpan/apidesc_test.go:87

## What Changes

- Refine RFC 25 drift detection: trigger, executor, schedule setting, result shape and audit entries.
- Run each drift check as a plan job kind from 109-job-kinds-state-operations.
- Keep the existing drift error code and API shape unchanged until the RFC is accepted.

Out of scope:

- Run model, job kinds, and runner execution
- Inventory queries beyond drift status
- Discovery and codification of unmanaged resources

Depends on:

- 109-job-kinds-state-operations

Builds on (not a change):

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- No user-visible change in this coarse step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks the drift shape in the API description test. (`pkg/dustpan/apidesc_test.go:87 (TestAPIDescriptionDrift)`)
- Main scheduler test waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The other rules of RFC 31 stay as proposals. They are one home for each value, plan before apply, and drift as a scheduled plan. The next ADR about the operator decides each of them."
- Effect: ADR 305 decides operator resources only. It leaves drift as a scheduled plan as a proposal for the operator. This change decides no server drift rule.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Background work runs on the primary installation."
- Effect: If drift checks run as durable work, they run only on the primary installation.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1)."
- Effect: If drift checks run as durable work, each claim needs a lease and a fencing token.
- Conflict: none.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values."
- Effect: A drift summary must use the masked form and never carry plain secret values.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18)."
- Effect: A drift schedule setting lives in the one settings resource with effective value and source.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What starts a drift check and where does it run before runners land?
   - Options: Scheduled plan or refresh per unit with a namespace default; Manual check only until runners land; External CI posts drift result.
   - Provisional decision (owner review 14 Nov): Manual check first, scheduled checks as a plan job kind after runners land.
   - Reason: RFC 25 waits on runs. Without an executor the schedule cannot run. No ADR decides this. Benchmark: AWS: scheduled drift checks with a visible last-check time per resource.
   - Persona impact: Developer sees stale state if no schedule exists; the manual check is the escape hatch. Escape hatch: Manual check stays available; no secret is exposed.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).
2. What does the drift read answer for each tool without merging tool models?
   - Options: Status plus last-check time and summary only; Full resource diff in the read; Link to the plan that found drift.
   - Provisional decision (owner review 14 Nov): Status plus last-check time and summary only.
   - Reason: Tool models stay separate and state reads mask secrets. Benchmark: PCI DSS: show what changed, when it was seen, and who can read it.
   - Persona impact: Developer needs a predictable read that never leaks secrets. Escape hatch: Masked read stays; reveal stays a separate call.
3. Which drift actions write audit entries and what schedule default shows its effective value?
   - Options: Audit each check start and end with schedule source; Audit only failures; No new audit entries.
   - Provisional decision (owner review 14 Nov): Audit each check with actor and schedule source; the schedule setting lives in the one settings resource.
   - Reason: Project rules need a record for each automatic action and a visible effective value. Benchmark: ISO 27001: record each automatic check, its actor and its schedule source.
   - Persona impact: Developer needs a record of why a unit shows drifted. Escape hatch: Manual check stays; schedule defaults to off.
   - Owner-tagged ADRs involved: ADR 212 (their words do not change).
4. How do no check yet, not found, soft-deleted unit, no grant, failed check, timeout, failover and concurrent start behave?
   - Options: Keep prior result and mark stale on failure, timeout, failover or missing grant; Clear result on failure; No result until first success.
   - Provisional decision (owner review 14 Nov): Keep prior result, mark stale on failure or failover, refuse without grant, conflict on concurrent start.
   - Reason: Coverage needs each empty and weak cell decided: console, refusal, audit, history, inventory state and backup. Benchmark: AWS: a timed-out check marks the result stale and names the retry.
   - Persona impact: Developer needs a clear stale state and the one next step. Escape hatch: Read of last result stays; failed check never hides the prior result.

Questions considered and answered:

- Does any ADR already decide drift detection? No. ADR 305 decision 8 keeps drift as a scheduled plan as a proposal for the operator. RFC 25 is the open question for the server.
- Where does scheduled drift work run? ADR 248 decision 14 and ADR 72 decision 2: durable work runs only on the primary installation under a claim lease.
- How are secret values kept out of drift results? ADR 41 decision 2: a state read masks secret values. A drift summary uses the masked form.
- Where does a drift schedule setting live? ADR 212 decision 7: the one settings resource shows effective value and source.
- What job kind runs a drift check? A drift check runs as a plan job kind from 109-job-kinds-state-operations. It does not add a new job kind.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
