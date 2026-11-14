---
version: 1
---

Issue: #1888.

## Problem

Developer starts a job without pool or code location and the server applies unit defaults. The job record does not name the values it took, so the developer cannot see or explain what ran.

## Evidence

- A job request with no pool or code location takes the unit defaults. Proof: Issue #1888: A job request with no pool or code location takes the unit's defaults (pkg/job/actions.go RequestRequirements).
- RequestRequirements exists on main in pkg/job. Proof: rg -n -F 'RequestRequirements' pkg internal cmd -> pkg/job/actions.go:109 (func RequestRequirements)
- Unit settings define a CodeLocation type on main. Proof: pkg/unitsettings/unitsettings.go:95 (type CodeLocation)

## Fix boundary

- Store effective pool ID and effective code location snapshot on each job at start
- Return the stored effective values on each read of the job record labeled as values at start
- Define server actor `server` and reason shape {rule, detail} and use RecordAutomaticAction for the entries this part writes

Out of scope:

- Trigger save-time gap checks, next run time display, skipped-slot records, and replaced-plan records
- Automatic token-disable audit entries
- Fail-closed identity default docs or explicit refusals
- Stored admin grant for bootstrap-created namespaces

## Check

- Add failing job start test for stored effective pool ID and code location in `NEW pkg/job/effective_test.go` Check: `go test ./pkg/job/ -run TestJobEffectiveValues -count=1` fails on main, passes after the fix
- Add failing audit test for server actor and reason shape in `NEW pkg/audit/automatic_test.go` Check: `go test ./pkg/audit/ -run TestAutomaticEntryActorReason -count=1` fails on main, passes after the fix
- Store effective pool ID and code location at StartJob and return the stored snapshot on job reads. Confirmed by: CI (`go test ./pkg/job/ -run TestJobEffectiveValues -count=1`). Check: `go test ./pkg/job/ ./pkg/audit/ -count=1`
- Wire effective values and RecordAutomaticAction through the existing audit entries route with its authorization actions. Confirmed by: CI (`go test ./pkg/audit/ -run TestAutomaticEntryActorReason -count=1`). Check: `GET /api/audit/entries` shows the server actor and reason for the applied default
- Document effective pool and code location plus the server actor and reason. Confirmed by: the driver's gates. Check: Docs page for job effective values renders and each command runs as written
- Run usability check from the docs page and full checks. A person's step. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- RequestRequirements computes extra actions for a job start request. (`pkg/job/actions.go:109 (func RequestRequirements)`)
- Unit settings define the CodeLocation shape used as the code location default. (`pkg/unitsettings/unitsettings.go:95 (type CodeLocation)`)
- Principal exposes the ActsAsPerson helper used for fail-closed identity defaults. (`pkg/identity/identity.go:134 (func Principal.ActsAsPerson)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Must the job record show the effective pool and code location when the request omits them? Yes. The issue rule for new work in #1888 answers it: every default shows as its effective value. The pack lists no ADR and no RFC for this split, so no ADR line answers it.
- Should this change also record trigger gaps or token disables? No. The roadmap scope for this part covers only pool and code location on jobs plus the server actor and reason shape for the entries this part writes. Trigger gaps, token disables, and identity defaults belong to the other three parts of the split of #1888.
- Does any ADR in the pack touch audit shape or job storage? No ADR text is in the pack. The pack lists adrs as empty and rfcs as empty, so there is no decision line to quote. An empty adr_check means no ADR in the pack is touched.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
