---
version: 1
---

Issue: #913, #1151, #2501, #2509.

## Problem

A developer runs the Pulumi CLI or OpenTofu against the server and wants misconfigurations caught on the plan in the same job, so a gate can block the change before apply without a separate CI lookup.

## Evidence

- Main resolves plan steps in the job service. Proof: pkg/dustpan/jobservice.go:753 (func planSteps) and pkg/dustpan/jobservice_internal_test.go:468 (TestPlanSteps)
- Main handles the runner plan phase in the runner API. Proof: internal/api/runnerapi/plan.go:81 (func Handler.planPhase)
- Main has gate deny error codes. Proof: gate.denied in internal/errcode/catalog_gate.go:17
- Issue 913 scopes scanner steps to the plan phase on the user runner with facts for gates. Proof: #913 scope: a scanner step declared in the job definition after the plan; its output as a job artifact and as typed facts for gates

## Fix boundary

- Write the RFC for the scanner step shape and the scanner fact schema.
- After the RFC and its ADRs are accepted, scanner steps run after the plan on the user runner.
- Scanner output is stored with the job and shown with the change order.
- Scanner outcome is exposed as typed facts that a gate can deny on.

Out of scope:

- Test reports in JUnit XML on jobs or units (issue 1151, needs its own RFC).
- Profile routing taint through exports.files and exports.variables (issue 2501, needs its own RFC).
- Profile open-time check of proxy secrets through imports (issue 2509).
- Any platform logic as a job such as backups or webhooks.

## Check

- Refine this change when its RFC is accepted: write the RFC for the scanner step shape and fact schema at the NEW path docs/rfc/0391-scanner-steps-in-the-plan-phase.md. Check: No route: the file docs/rfc/0391-scanner-steps-in-the-plan-phase.md exists and `make docs-lint` passes on it.

Depends on:

- #899
- `test-no-gate-schema-has` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main resolves tool plan steps for jobs. (`pkg/dustpan/jobservice.go:753 (func planSteps)`)
- Main handles the runner plan phase. (`internal/api/runnerapi/plan.go:81 (func Handler.planPhase)`)
- Main has gate deny error codes. (`internal/errcode/catalog_gate.go:17 (gate.denied)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 4 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "A job is a list of phases. A phase is the work that one executor does with one claim."
- Effect: Scanner steps stay inside the plan phase. No new phase kind is added now.
- Conflict: none.

**ADR 134: The launcher contract** (owner-tagged: decision 5).

- Decisions that apply: 5 (current).
- Changed by: ADR 199 changes decisions 4 and 6; ADR 330 extends decision 6. Chain to the end: ADR 199, ADR 330, ADR 286, ADR 310, ADR 353.
- Current rule: this ADR, with ADR 199, 330.
- Quote: "A step is an argument list (owner). The launcher starts `Args[0]` with `Args[1:]`, with no shell."
- Effect: The RFC keeps the argument-list shape with no shell. No step is added until the owner accepts the step shape.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 3).

- Decisions that apply: 3 (extended).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "A missing fact is a typed value (owner). Each fact always has a value of its type."
- Effect: A scanner fact group carries an available flag and fails closed. A new schema version needs an RFC first.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The build that follows the RFC ships its docs page in the same pull request.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the step shape of a scanner step in the launcher contract?
   - Options: A launcher step with the argument-list shape, no tool credential, same isolation as the plan; A new launcher role with its own credential path.
   - Provisional decision (owner review 14 Nov): A launcher step with the argument-list shape, no tool credential, same isolation as the plan
   - Reason: It keeps the owner-tagged argument-list rule and adds no credential path. Benchmark: Google: executor contracts fix step shape and isolation in the interface before use.
   - Persona impact: Runner operators get no new credential path; developers declare scanners in the unit settings. Escape hatch: No scanner step runs until the RFC is accepted.
   - Owner-tagged ADRs involved: ADR 134 (their words do not change).
2. Which fact schema version carries scanner results at the plan decision point?
   - Options: A new group scannerFindings with an available flag in a new minor version of the plan schema, inside the 256 KiB limit; Reuse the resource policy facts.
   - Provisional decision (owner review 14 Nov): A new group scannerFindings with an available flag in a new minor version of the plan schema, inside the 256 KiB limit
   - Reason: It keeps one schema per decision point and does not touch the built-in gates. Benchmark: AWS: policy checks read typed findings with a present flag and fail closed on missing data.
   - Persona impact: Namespace administrators write gates on typed counts, not on log text. Escape hatch: A gate reads available false and fails closed until the version exists.
   - Owner-tagged ADRs involved: ADR 226 (their words do not change).

Questions considered and answered:

- Where does a scanner step run? On the user runner under the launcher contract (ADR 134 decision 5). The exact step shape is an owner question.
- How does a gate see the result? Through a typed fact group (ADR 226 decisions 2, 3) with an available flag. The schema version is an owner question.
- Does the server run the scanner? No. Issue 913 scopes the step to the user runner under the launcher contract.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
