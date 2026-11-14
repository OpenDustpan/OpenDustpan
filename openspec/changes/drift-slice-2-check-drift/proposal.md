---
version: 1
---

Issue: #1181, #1187, #1576, #2185.

## Problem

A developer with a stack of the Pulumi CLI wants to check drift on demand after an out-of-band change, read drifted or in-sync, and trust that the check changes nothing.

## Evidence

- Main has one OpenTofu drift argv table in pkg/job and a second builder in pkg/dustpan. Proof: pkg/job/driftsteps.go:12 driftArgv; pkg/dustpan/jobservice.go:874 driftSteps; pkg/dustpan/jobservice.go:888 tofuStep
- The runner validates every drift check with the shared validator, whatever the tool. Proof: pkg/runner/executor.go:58 validate = job.ValidateDriftSteps
- The e2e suite starts only opentofu jobs. Proof: issue 2185 evidence: e2e/jobs_api_test.go lines 100 and 399-408 start only opentofu jobs
- The job start route passes any tool through UnitObject. Proof: issue 2185 evidence: internal/api/jobs/start.go:291
- The drift status drifted exists. Proof: pkg/drift/drift.go:65 StatusDrifted

## Fix boundary

- Fixed Pulumi refresh preview steps, owned by a per-tool table in pkg/job.
- The shared validator reads the same per-tool table; no second argv list.
- A pulumi start opens only for purpose drift-check; other pulumi purposes stay job.unsupported.
- The runner reduces the Pulumi preview to addresses and kinds; a failed check keeps the last result.
- An e2e step with the real Pulumi CLI, and docs words refresh preview.

Out of scope:

- OpenTofu drift result behavior changes
- Drift schedule and notifications
- Pulumi plan, apply and destroy jobs
- Remediation

## Check

- Write failing validator tests in pkg/job/driftsteps_test.go (NEW) for RegisterDriftSteps and ValidateDriftSteps: registered Pulumi steps pass, edited steps, unknown tool and empty table fail. Check: `go test ./pkg/job -run TestValidateDriftStepsPulumi -count=1` NEW test fails on main, passes after.
- Write a failing planner test in pkg/dustpan/jobservice_driftsteps_internal_test.go (NEW): the planned Pulumi steps pass the validator. Check: `go test ./pkg/dustpan -run TestPulumiDriftStepsMatchValidator -count=1` NEW test fails on main, passes after.
- Write the e2e step in e2e/drift_pulumi_test.go (NEW): change a resource out of band, start the check, read drifted, then a clean check reads in-sync. Route POST drift-checks, gate unit:run-plan, access token. Check: `make e2e` runs the NEW step in e2e/drift_pulumi_test.go; it fails on main. BLOCKED by spike 1187 (machine form in RFC 25).
- Move the OpenTofu argv from pkg/job/driftsteps.go into pkg/job/drift_opentofu.go (NEW), add pkg/job/drift_pulumi.go (NEW) and RegisterDriftSteps, and select the tool key in pkg/dustpan/jobservice.go (func driftSteps). Check: `go test ./pkg/job ./pkg/dustpan ./pkg/runner -count=1` passes; OpenTofu steps still pass.
- Refuse a pulumi start with a purpose other than drift-check in internal/api/jobs/start.go with the error code job.unsupported. Check: `go test ./internal/api/jobs -run TestStartRefusesPulumiNonDrift -count=1` NEW test passes.
- Reduce the Pulumi preview to addresses and kinds in pkg/runner/executor.go and the drift result path, and add the words refresh preview to docs/reference/jobs-api.md. Check: `go test ./pkg/runner -run TestPulumiPreviewReduction -count=1` NEW test passes; BLOCKED by spike 1187 for the machine form.
- Wiring through POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks (action unit:run-plan, access token), the e2e step in e2e/drift_pulumi_test.go, the docs page, a usability check from the docs page alone, and make check. Check: `make e2e` passes; `make check` reports zero findings.

Depends on:

- drift slice 1 (purpose, store, routes, summary format)
- spike 1187: machine form and no-hook proof written into RFC 25

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The shared validator checks drift steps with the OpenTofu argv. (`pkg/job/driftsteps.go:37 (func ValidateDriftSteps)`)
- The runner calls the shared validator for each drift check. (`pkg/runner/executor.go:58 (func Executor.Phases)`)
- The server builds drift steps per tool. (`pkg/dustpan/jobservice.go:874 (func driftSteps)`)
- The server builds OpenTofu steps with tofuStep. (`pkg/dustpan/jobservice.go:888 (func tofuStep)`)
- The error code job.unsupported exists. (`internal/errcode/catalog_job.go:22`)
- The error code drift.start-conflict exists. (`internal/errcode/catalog_drift.go:8`)
- The status drifted exists. (`pkg/drift/drift.go:65`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 12: A backend for many infrastructure-as-code tools** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 13 extends; ADR 15 extends; ADR 35 changes; ADR 209 changes; ADR 248 changes; ADR 305 extends. Chain to the end: ADR 13, ADR 15, ADR 35, ADR 209, ADR 248, ADR 305, ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 272, ADR 286, ADR 47, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 364, ADR 153, ADR 346, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 13, 15, 35, 209, 248, 305.
- Quote: "Nothing enters the core because only one tool needs it."
- Quote: "A tool is supported, and listed as such, only when the e2e suite drives its
  real CLI across a version matrix and the server passes those steps."
- Effect: Per-tool steps move out of a shared list into per-tool tables, and Pulumi use opens only with the real CLI e2e step.
- Conflict: none.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 1 (current), 12 (changed), 13 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "OpenTofu first, then the Pulumi CLI. The Pulumi CLI path starts after a separate e2e spike on the latest CLI."
- Quote: "The runner reduces the plan to addresses and kinds. For a drift check, the server reads no state. A spike must prove that a unit with client-side encryption still gives the addresses."
- Effect: This change builds the Pulumi path as a refresh preview job, with the runner reduction, once the spike result exists.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which machine form of pulumi refresh --preview-only gives the drift summary without parsing text, and does refresh run hooks or command resources with and without --run-program?
   - Options: Use pulumi refresh --preview-only --json for the machine diff; Use the state journal replay of the preview for the machine diff.
   - Provisional decision (owner review 14 Nov): Use the form that spike 1187 proves on the latest Pulumi CLI and writes into RFC 25. Start this change only after that.
   - Reason: ADR 263 decisions 12 and 13 wait for this proof, so the answer is a dependency, not a design choice of this change. Benchmark: AWS practice: compare a machine-readable diff for drift, never screen text.
   - Persona impact: No persona loses work. Pulumi drift checks stay refused until the proof exists. Escape hatch: A preview form that the spike did not prove fails closed with no result change.

Questions considered and answered:

- How does a runner step read a difference from a Pulumi refresh preview? ADR 263 decision 13 waits for the spike. Spike 1187 pins the machine form. The tasks that need it say BLOCKED.
- Where do the Pulumi drift command lines live without breaking the shared core? ADR 12 forbids tool-only lists in shared code. Per-tool tables sit in per-tool files behind one registry that the planner and the runner both read.
- Does this slice open all Pulumi jobs? No. ADR 12 needs a real CLI e2e step per supported use. Only purpose drift-check opens; other pulumi purposes stay refused.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
