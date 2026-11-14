---
version: 1
---

## Why

**Persona: developer.** A Developer finds an unmanaged resource in the inventory and wants starter code that brings it under management in a stack or workspace, without hand-writing every block from the cloud state.

The user path this change must make work:

1. **Docs page.** A Developer reads the future guide page for code output for unmanaged resources and its limits. The page ships with the refinement.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A Developer reads managed resources of a version that does not exist and the read fails. | The server answers 404 with the error code for an unknown state version and names the check for the version. | `GET /api/v2/state-versions/{id}/resources` |

Evidence for the claims above:

- This item waits on the sweep plugin milestone for slices 0 to 6 and the agents milestone for slices 7 and 8. Proof: docs/roadmap.md:827 row: RFC 124 Code for unmanaged resources waits on the sweep plugin milestone (RFC 123) for slices 0 to 6; the agents milestone (milestone 10) for slices 7 and 8

## What Changes

- Record RFC 124 as a coarse Later change with no behavior yet: slices 0 to 6 wait on the sweep plugin milestone (RFC 123), slices 7 and 8 wait on the agents milestone.

Out of scope:

- Sweep plugins (RFC 123 slices 0 to 6)
- Agent milestone slices 7 and 8
- Sweep schedules, targets, and findings lifecycle

Depends on:

- sweep plugin milestone (RFC 123) slices 0 to 6
- agents milestone (milestone 10) slices 7 and 8
- 47-discovery-codification
- `47-discovery-codification` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- No user path ships in this coarse change, so no new cost or surprise is added.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs durable schedules with a first-delay wait before the first claim. (`pkg/durable/scheduler_test.go:175 (func TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main refuses a delete guarded by a resourced inline checkpoint. (`pkg/stack/delete_guard_test.go:393 (func TestDeleteServiceRefusesResourcedInlineCheckpoint)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 5).

- Decisions that apply: 5 (changed).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A pool for sweeps is read-only (owner). A pool for sweeps carries a read-only identity: the workload identity of its hosts, or the read-only entry of a profile (ADR 164 decision 4)."
- Effect: Code output must not widen the read-only sweep identity.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep is not a job. A job belongs to one unit, and the one-change rule, the phases and the gates of ADR 100 serve changes to a unit."
- Effect: A sweep changes nothing and has no unit; code output stays a later reviewed step applied through a unit.
- Conflict: none.

**ADR 218: What a namespace is for, and when to make a namespace or a collection (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 5 (changed).
- Changed by: ADR 248 changes decisions 1, 2 and 5; ADR 286 changes. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248, 286.
- Quote: "Make a new namespace when the units need a boundary of their own."
- Effect: Code output stays inside one namespace and never shares profiles, blueprints or classification across namespaces.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Sweep work runs on a runner; this coarse change adds no new place where work runs.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Where does the data for code output come from: stored finding data or a new cloud read?
   - Options: Stored finding data only, with no new cloud read; New cloud read on a runner under a separate authorization; Stored data first, with an explicit refresh read when the finding is stale.
   - Provisional decision (owner review 14 Nov): Stored finding data only, with no new cloud read
   - Reason: AWS keeps code synthesis on stored inventory data and starts a new read only as an explicit refresh with its own authorization. Benchmark: AWS
   - Persona impact: No persona is upset: no behavior ships now. Escape hatch: No code is generated until the refinement is accepted.
2. Which authorization action and audit entry does code output need?
   - Options: New unit action plus one audit entry per request; Reuse unit:read with no audit entry; Reuse unit:read with one audit entry per request.
   - Provisional decision (owner review 14 Nov): New unit action plus one audit entry per request
   - Reason: Every endpoint declares its actions and each read of cloud-derived data writes a record. Benchmark: AWS
   - Persona impact: No persona is upset: no route ships now. Escape hatch: No route ships until the refinement fixes the action.
3. How does code output handle secret-bearing attributes and stale, ignored, deleted, already-managed or unknown-type findings?
   - Options: Mask secrets, refuse the other cases with an error code; Omit secrets, warn on the other cases; Refuse whenever a case applies.
   - Provisional decision (owner review 14 Nov): Mask secrets, refuse the other cases with an error code
   - Reason: SOC 2 style masking with an audited reveal keeps secrets out of output; refusal avoids duplicate resources. Benchmark: SOC 2
   - Persona impact: No persona is upset: no output ships now. Escape hatch: No code is generated until the refinement fixes the rules.

Questions considered and answered:

- Does code output conflict with the read-only sweep model No. ADR 164 decision 2 keeps sweeps read-only. Code output is a later step that a person reviews and applies through a unit, not part of the sweep.
- How do secret values stay masked in code output ADR 41 decisions 2 and 8 mask secret values in state reads. The refinement fixes masking and the audited reveal for code output.
- Where does the data for code output come from No ADR in the pack states what a finding stores or whether a new cloud read is allowed, so the data source stays an open question for the refinement.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
