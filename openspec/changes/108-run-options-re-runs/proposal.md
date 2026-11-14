---
version: 1
---

## Why

**Persona: developer.** A Developer wants to start a run with clear options, run it again the same way, and pass small variables without editing source. Today there is no run object, so there is nothing to set, repeat or pass.

The user path this change must make work:

1. **Docs page.** Developer reads the run options page after the run model ships
2. **API.** Developer re-runs a prior run by its ID after the run model ships
3. **CLI.** Developer passes small non-secret variables to one run after the run model ships

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer asks for run options before the run model exists | The server answers 404 with the NEW error code `run.no-such-run`. | `make e2e` |

Evidence for the claims above:

- The roadmap places RFC 108 after runs in milestone 5. Proof: docs/roadmap.md:812 | [RFC 108](rfc/0108-run-options-re-runs-and-run-variables.md) Run options, re-runs and run variables | runs (milestone 5)

## What Changes

- Name the closed set of run options once the run model exists
- Name the re-run rule once the run model exists
- Name the shape and limits of run variables once the run model exists

Out of scope:

- The run model itself from milestone 5
- Run limits and the run queue
- Job kinds for state operations
- Step results, hooks, approval steps and scanners

Depends on:

- runs (milestone 5): the run model that carries options, re-run links and variables

## Usability

Where the path still costs the person time or surprises them:

- Until the run model exists, a Developer cannot preview what an option or a re-run costs.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that a backup target comes only from the option (`pkg/dustpan/dustpan_test.go:1268 (TestBackupTargetComesOnlyFromTheOption)`)
- Main renders variables in the job proxy (`pkg/jobproxy/render_test.go:45 (TestRenderFillsVariables)`)
- Main tests scheduler delay before first claim (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values. A state read answers the structure and the values of a state. It replaces each secret value with a marker."
- Effect: Run variables carry no secret values. Secret values stay masked in state reads.
- Conflict: none.

**ADR 47: Soft delete, and a unit never moves between namespaces** (not owner-tagged).

- Decisions that apply: 1 (extended), 5 (extended).
- Changed by: ADR 151 changes decision 3; ADR 153 extends decisions 1 and 3; ADR 346 extends decisions 5 and 7. Chain to the end: ADR 151, ADR 153, ADR 346, ADR 154, ADR 210, ADR 217, ADR 220, ADR 213, ADR 248, ADR 309, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 300, ADR 353, ADR 315, ADR 356, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 151, 153, 346.
- Quote: "A delete is a soft delete. The server keeps the state of a deleted unit for a retention period. During this period a person can restore the unit."
- Quote: "A unit never moves between namespaces. The server has no action and no endpoint for it. A unit belongs to its namespace."
- Effect: A re-run names its source unit by stable ID and refuses a deleted unit. Units stay in their namespace.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Quote: "A write-path installation check. Every write transaction reads the installation state in the same statement that reads its namespace fence."
- Effect: Runs are durable operations with a lease and a fence check. Options, re-runs and variables attach to that model when it ships.
- Conflict: none.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The work runs through OpenDustpan jobs and gates (owner). The operator does not run a tool. It creates units and requests jobs through the REST API (ADR 17)."
- Effect: Run options, re-runs and variables ride on jobs requested through the REST API. This change adds no new execution path.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the run model that options, re-runs and variables attach to, and is this change blocked until milestone 5 ships it?
   - Options: Wait for the run model of milestone 5, then refine RFC 108; Define run options now before the run model exists.
   - Provisional decision (owner review 14 Nov): Wait for the run model of milestone 5, then refine RFC 108
   - Reason: Naming options before the run object exists would fix names the run model may reject. Benchmark: AWS: re-run repeats the exact prior inputs and records the source run ID
   - Persona impact: Developer cannot set options, re-run, or pass variables until the run model ships; nothing else changes. Escape hatch: none: no work starts until the run model ships.
2. What is the closed set of run options for one run?
   - Options: A closed allowlist of run options fixed by the refinement; An open map of option names and values.
   - Provisional decision (owner review 14 Nov): A closed allowlist of run options fixed by the refinement
   - Reason: A closed allowlist keeps validation and docs exact across both tools. Benchmark: AWS: a closed set of per-run options with defaults shown in the API
   - Persona impact: Developer sees the same option names on every run; an open map would surprise tool CLIs. Escape hatch: none: no options exist until the run model ships.
3. What is the shape and what are the limits of run variables?
   - Options: Plain-text variables with name, value and count limits and no secret values; Variables that may also carry secret values.
   - Provisional decision (owner review 14 Nov): Plain-text variables with name, value and count limits and no secret values
   - Reason: Log-safe plain text with limits avoids secret leaks through logs and history. Benchmark: ISO 27001: variables carry non-secret text only with size and count limits
   - Persona impact: Developer passes small plain-text values; secrets stay masked and audited. Escape hatch: none: secret values stay in profiles, never in variables.

Questions considered and answered:

- Does any accepted ADR already decide run options, re-runs or run variables? No. The pack names no accepted run model. ADR 305 decision 4 only says work runs through jobs and the REST API. ADR 72 decides leases and the fence check for durable operations. The change waits for milestone 5.
- Can run variables carry secrets? No. ADR 41 decision 2 masks secret values. Variables are log-safe plain text only with no secret values.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
