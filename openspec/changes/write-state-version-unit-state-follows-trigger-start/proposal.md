---
version: 6
---

Issue: #1211.

## Why

**Persona: developer.** A Developer keeps unit B in sync with unit A. The Developer sets a follows trigger on B, applies A, and expects one plan of B per applied version, with no extra runs and no silent skip.

The user path this change must make work:

1. **API.** Create a follows trigger on unit B with source unit A and when applied, through the trigger routes.
2. **CLI.** Apply unit A with the real OpenTofu CLI from a workstation or through a job.
3. **API.** Watch the plan job of unit B start once for the new final version and read the live log.
4. **Docs page.** Read the docs page that names which runs count as an apply and the read rule of the last saver.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Event for a version older than the last final version of the unit. | The server returns ErrVersionMismatch and starts nothing. Read the trigger to see its last state. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Job service refuses the plan for one follower. | The server records the error on that trigger and continues with the other followers. Fix the unit settings or pool, then fire the version again by hand. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Follows plan reads a source output and the last saver lacks the grant. | The server denies the read and writes an audit entry. Ask a namespace administrator to change the grants of the last saver. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |

Evidence for the claims above:

- The trigger dispatcher port and its conformance harness already exist. Proof: pkg/trigger/ports.go:136:// Outcome is the result of one trigger in a Dispatcher.Upstream call. and pkg/trigger/triggertest/dispatcher.go:76:out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
- The version-mismatch sentinel already exists. Proof: pkg/trigger/trigger.go:48 and pkg/trigger/dispatcher.go:129:return nil, ErrVersionMismatch
- Trigger rows already carry the last saver subject and stable principal ID. Proof: pkg/trigger/trigger.go:190:LastSaver string and pkg/trigger/trigger.go:192:LastSaverID string

## What Changes

- Dispatcher.Upstream verifies the final state version, enforces one plan per trigger and version, returns ErrVersionMismatch for an older version, and records per-trigger start errors.
- The OpenTofu final-version write sends the upstream event. Pulumi sends nothing in this slice.
- The job carries the trigger ID. Each read of a source output checks the current grants of the last saver of the trigger, and denies when the saver is missing, deleted or disabled.
- Follows triggers publish a declared dependency edge for the graph and safe delete.
- The trigger fire route with a final version refires missed followers with the same idempotency key.
- A docs page names which runs count as an apply.

Out of scope:

- Run-time follower caps and run-time depth stop
- The when value outputs-changed and its changed-outputs filter
- Pulumi update sender
- Schedule and review-request-push triggers

Depends on:

- write-state-version-unit-state-1
- job-service-unit-settings-pool
- output-sharing
- `write-state-version-unit-state-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A Developer who expects a plan after every run is surprised that a plan with no state change starts nothing; the docs page says only a final state version counts.
- A refire needs the version ID from unit history; the error answers name the trigger read route.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Dispatcher.Upstream is the fan-out entry point. (`pkg/trigger/ports.go:192`)
- ErrVersionMismatch reports an event that is not a final state version of the unit. (`pkg/trigger/trigger.go:48`)
- Trigger rows carry LastSaver and LastSaverID. (`pkg/trigger/trigger.go:190`)
- A test of the final-version read path exists. (`pkg/dustpan/triggers_versions_internal_test.go:36`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current), 11 (current), 12 (current), 13 (current), 14 (changed), 15 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14; ADR 369 changes decision 14; ADR 370 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 286, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 369, 370.
- Quote: "A `follows` trigger has a field `when` (question 2). The default is `applied`: after each apply of the source. The value `outputs-changed` comes in a later slice."
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind."
- Effect: The change implements the applied start, the declared edge, the plan-only start and the trigger source kind. Reads follow decision 14 as changed by the output sharing decision.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets` for a secret output."
- Effect: The read uses the last saver of the trigger with unit:read-outputs and unit:read-secrets, fail closed.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What counts as an apply? ADR 267 decision 12: for OpenTofu any final state version counts.
- When does a follows trigger fire? ADR 267 decision 13: when applied by default; outputs-changed is out of scope.
- Whose grants govern source output reads? ADR 273 decision 5 and ADR 267 decision 14: the last saver, checked at each read.
- How does fan-out stay safe without a shared transaction? A per-trigger idempotency key per version replays the first job after a crash.
- Does a follows trigger cross namespaces? ADR 267 decision 21: no.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
