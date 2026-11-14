---
version: 1
---

Issue: #2208, #2209, #2898.

## Problem

A runner operator runs a job that uses a job token against an OpenTofu backend, but the lock fails with 403 because the gate subject has no job, so the job cannot run.

## Evidence

- principalSubjects.Subject gives the Subject of a principal from its credential. Proof: pkg/dustpan/stateguard.go:335 (func principalSubjects.Subject)
- jobTokenWrite decides a write at state.write with a job token. Proof: pkg/gate/service.go:261 (func jobTokenWrite)
- TFE declare path is createStateVersion. Proof: internal/api/tfe/state_write.go:168 (func Handler.createStateVersion)
- The gate already carries PassedApply and PlanLock for the job-token path. Proof: pkg/gate/service.go:274

## Fix boundary

- Fill Subject.Job for a job token with job ID, phase, units, and PassedApply and fail closed on read failure.
- Allow a job token only its plan lock or, after PassedApply, the state of its own job.
- Check a TFE unlocked declare at state.write with unlocked-write before the transaction.

Out of scope:

- Runner job execution beyond the gate subject and the unlocked declare.
- New decision points or gate syntax.
- HTTP backend LOCK and UNLOCK behavior beyond the same GuardStateWrite subject fix.

## Check

- Add failing gate subject test through principalSubjects.Subject in pkg/gate/service_test.go. Check: TestGateJobTokenSubjectFromProductionSource in pkg/gate/service_test.go fails on main, passes after fix.
- Add failing TFE unlocked declare test with deny gate in internal/api/tfe/state_write_test.go. Check: TestTfeUnlockedDeclareMeetsStateWrite in internal/api/tfe/state_write_test.go fails on main, passes after fix.
- Add failing PassedApply hostile tests before pass and after fail in pkg/gate/service_test.go. Check: TestJobTokenWriteNeedsPassedApply in pkg/gate/service_test.go fails on main, passes after fix.
- Fill Subject.Job with job ID, phase, units, and PassedApply and fail closed in pkg/dustpan/stateguard.go:335. Check: pkg/dustpan/stateguard.go builds and go test ./pkg/dustpan ./pkg/gate passes.
- Evaluate PassedApply in jobTokenWrite with coded audited refusal in pkg/gate/service.go:261. Check: go test ./pkg/gate passes including PassedApply allow and refuse paths.
- Record the NEW phase flag PassedApply when a phase passes change.apply in pkg/gate/service.go:261. Check: Phase record persists across restart and go test ./pkg/job passes.
- Call state guard with unlocked-write and unit:write-unlocked in createStateVersion on unlocked override in internal/api/tfe/state_write.go:168. Check: go test ./internal/api/tfe passes and refusal is JSON:API 403.
- Add e2e step with a job token lock of its plan via POST /api/v2/workspaces/{id}/actions/lock past the state.write gate with a job-token credential. Confirmed by: CI (make e2e). Check: E2E job token takes its plan lock via POST /api/v2/workspaces/{id}/actions/lock past the state.write gate with a job-token credential.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service.GuardStateWrite evaluates state.write gates. (`pkg/gate/statewrite.go:65 (func Service.GuardStateWrite)`)
- principalSubjects.Subject gives the Subject of a principal. (`pkg/dustpan/stateguard.go:335 (func principalSubjects.Subject)`)
- jobTokenWrite decides a write at state.write with a job token. (`pkg/gate/service.go:261 (func jobTokenWrite)`)
- createStateVersion serves the TFE state-version declare. (`internal/api/tfe/state_write.go:168 (func Handler.createStateVersion)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 224: Gates at fixed decision points** (owner-tagged: decision 16).

- Decisions that apply: 16 (changed).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 changes decision 16; ADR 335 extends decision 9; ADR 335 extends decision 13; ADR 342 changes decision 16; ADR 353 changes decision 14; ADR 353 extends decision 2; ADR 354 extends. Chain to the end: ADR 241, ADR 286, ADR 313, ADR 317, ADR 335, ADR 342, ADR 353, ADR 354, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354.
- Quote: "`state.write`: writes from a person's own CLI (owner, 2026-10-18). Version 1 evaluates the gates of `state.write` before each of these writes:"
- Effect: Follows the rule that each tool route that writes state meets state.write; fills Subject.Job so the job-token path can be evaluated instead of denied on empty job.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An unlocked state version on the TFE-compatible API (owner, 2026-11-07). Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`."
- Effect: Applies the decided unlocked-write check to the TFE declare path that skips the lock.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the TFE unlocked declare need an RFC to extend the ADR 224 table? ADR 335 decision 3 already decides it, so no RFC is needed.
- How is PassedApply evaluated for an apply job token? ADR 224 decisions 13 and 16 plus ADR 335 decision 1: fail closed, keep deny effect, and evaluate the stored PassedApply value in jobTokenWrite.
- Which job-token writes are in scope? Only the plan lock of its own job and the state write of its own job after PassedApply; force-unlock, restore, and delete stay refused for a job token.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
