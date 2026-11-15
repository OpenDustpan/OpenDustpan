---
version: 2
---

## Why

**Persona: runner operator.** A runner operator runs state work such as refresh and unlock as jobs. The state-operation kinds and the many-run behavior wait on runs, so the operator cannot plan pools and labels for them.

The user path this change must make work:

1. **API.** Runner operator claims jobs for state operations after the run model ships.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A runner operator cannot tell why a state-operation job was refused. | The server answers with the error and names the one command that shows the state of the server. | `opendustpan status` |

## What Changes

- Name the state operations that become job kinds after runs ship.
- Name the many-run behavior after runs ship.

Out of scope:

- Run model in milestone 5
- Sibling RFCs 107, 108 and 110, each with its own change

Depends on:

- none

Builds on (not a change):

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes in this coarse change.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the audit kind tool-operation. (`pkg/audit/audit.go:44`)
- Main has a scheduler test that waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values. A state read answers the structure and the values of a state. It replaces each secret value with a marker."
- Effect: Any state-operation job output that reads state masks secrets. Reveal stays a separate call.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Effect: State-operation jobs run as durable operations with a lease and a fencing token.
- Conflict: none.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The work runs through OpenDustpan jobs and gates (owner)."
- Effect: State-operation kinds run through jobs and gates. This change alters no operator rule.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which state operations become job kinds?
   - Options: State refresh only; State refresh, state unlock, state replace and state delete; Open set of state operations.
   - Provisional decision (owner review 14 Nov): State refresh, state unlock, state replace and state delete, each with its own authorization action, audit entry and masked output. Restore stays out.
   - Reason: A closed set keeps authorization, audit and retention reviewable, as AWS does with named operation types. Benchmark: AWS
   - Persona impact: A closed set upsets no persona because each kind uses the same job path. Escape hatch: Ship refresh first and add the other kinds later on the same job path.
2. What does many runs mean for RFC 109, including partial failure, lease takeover and a lock held by another run?
   - Options: One job per unit only; One job that fans out across many runs with per-unit results; Many runs only through separate jobs.
   - Provisional decision (owner review 14 Nov): One job that fans out with per-unit results, leases, audit records, fence and maintenance checks, and a job result that is failed when any unit fails.
   - Reason: Google Cloud Batch keeps one job with many tasks and reports each task. Benchmark: Google
   - Persona impact: Runner operators keep per-unit leases and labels either way. Escape hatch: Ship one unit per job first and add fan-out later.

Questions considered and answered:

- Does this change define job kinds now? No. The roadmap says it waits on runs in milestone 5, so kinds, actions and many-run behavior are refined after the run model ships.
- Which ADRs bound the later RFC? ADR 305 decision 4 says work runs through jobs and gates. ADR 72 decision 2 gives a lease per operation. ADR 41 decision 2 masks secrets.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
