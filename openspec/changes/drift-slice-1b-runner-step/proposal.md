---
version: 1
---

Issue: #1375, #1376, #1391, #1396.

## Problem

A short-form change. A finished drift check of an OpenTofu workspace gives no drift result today, because no runner step reduces the plan and nothing calls the drift store. The unit never reads drifted or in-sync from the drift route.

## Evidence

- No non-test code calls drift.Store.Record on main. Proof: rg -n -F --glob '!*_test.go' 'drift.Store.Record' pkg internal cmd gives 0 matches.
- The drift.Store port and the drifted status exist on main. Proof: pkg/drift/ports.go:19 and pkg/drift/drift.go:65.
- The job.ArtifactStore type is not used outside its suite on main. Proof: rg -n -F --glob '!*_test.go' 'job.ArtifactStore' pkg internal cmd gives 1 match, in pkg/job/jobtest/jobtest.go:13.

## Fix boundary

- A new runner step reduces the refresh-only plan to a summary of addresses and kinds (changed or deleted), cut at 1000 addresses with a truncated flag.
- The runner leaves the summary as an artifact of the job, tied to the job token of the phase claim; transport waits for #1374.
- A new reconcile validates the summary at the end of the plan phase and calls drift.Store.Record with the phase end time.
- A spike test shows that a unit with client-side encryption still gives addresses with no secret values.
- The drift docs page drops the sentence that says no runner records a result.

Out of scope:

- The e2e step with the real OpenTofu CLI (slice 1c).
- Auto-dedrift.
- The runner session routes and the launcher binary.
- The Pulumi CLI drift path.
- The jobId visibility for a caller without job:read, which belongs to slice 1a.
- A later state-version write that sets status unknown, which belongs to slice 1a.

## Check

- Write the failing reduction test in NEW pkg/drift/summary_test.go. Check: TestReducePlanToDriftSummary fails on main. No route: the test reads a recorded plan file only.
- Write the failing hostile summary tests in NEW pkg/drift/summary_test.go. Check: TestHostileSummaryChangesNothing fails on main. No route: the test calls Validate only.
- Write the failing reconcile test in NEW internal/driftreconcile/reconcile_test.go. Check: TestReconcileRecordsAtPhaseEnd fails on main. No route: the test drives the reconcile with a fake job token only.
- Write the spike test for client-side encryption in NEW pkg/drift/encrypted_spike_test.go. Check: TestEncryptedUnitGivesAddresses fails on main. No route: the test checks addresses only.
- Write the failing idempotence test for double reconcile in NEW internal/driftreconcile/reconcile_test.go. Check: TestDoubleReconcileRecordsOnce fails on main. No route: the test replays Record with the same job ID only.
- Write Validate in NEW pkg/drift/summary.go and the reconcile in NEW internal/driftreconcile/reconcile.go, wired in pkg/dustpan/drift.go:21 (func WithDriftStore). Check: The drift read route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token reads drifted after a valid summary; runner transport is BLOCKED by #1374.
- Edit the existing drift docs page at NEW docs/guides/drift.md and its reference page to remove the sentence that says no runner records a result. Check: The existing drift docs page holds no sentence that says no runner records a result.
- Wire the reconcile to the drift routes with their authorization actions. Check: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift with an access token, gated by unit:run-plan at start.

Depends on:

- #1180 slice 1a
- #1374 runner session routes and launcher binary
- #1163 job.Recorder and job.ArtifactStore

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The drift.Store port exists. (`pkg/drift/ports.go:19`)
- The option WithDriftStore exists. (`pkg/dustpan/drift.go:21 (func WithDriftStore)`)
- TestDriftAPI from slice 1a exists. (`e2e/drift_api_test.go:98`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (current), 11 (current), 12 (changed), 17 (current), 20 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "A drift check is a job. It has the operation `refresh` and one phase, `plan`. For OpenTofu it is a refresh-only plan. For the Pulumi CLI it is a refresh preview (ADR 258). It changes no resource and writes no state version. The phase table of ADR 100"
- Quote: "The server stores the drift result. The result holds the addresses of the resources that differ. The unit keeps the result of its latest check. The server sets the time of the result from the end of the plan phase. It does not take the time from the"
- Quote: "A failed check keeps the last result. A check that fails or is canceled does not change the result or the time of the unit. The failure shows as a failed job. When another job writes a state version of the unit, the status becomes `unknown`. This sta"
- Quote: "The runner reduces the plan to addresses and kinds. For a drift check, the server reads no state. A spike must prove that a unit with client-side encryption still gives the addresses."
- Quote: "Actions. A manual check needs the action `unit:run-plan`. A change to the setting needs the settings action of the object. This adds no action."
- Quote: "A drift check keeps the default state lock. The check does not use `-lock=false`. A check that cannot get the lock fails, and decision 11 keeps the last result. Benchmark: hosted assessments do not run while a run is active."
- Effect: The runner step reduces the plan to addresses and kinds, the server sets result time from phase end, a failed check keeps the last result, start needs unit:run-plan, and the check keeps the default state lock.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which action gates the check and Record? ADR 263 decision 17: the start route needs unit:run-plan. Record at phase end has no caller; the reconcile checks purpose drift-check, succeeded state, same unit, and fence.
- Who sets the result time? ADR 263 decision 3: the server ignores runner time and stores the phase end time.
- What happens after a failed or invalid check? ADR 263 decision 11: the last result and time stay unchanged, and the job shows as failed.
- What does the runner produce? ADR 263 decision 12: the runner reduces the plan to addresses and kinds, and a spike proves addresses for client-side encryption.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
