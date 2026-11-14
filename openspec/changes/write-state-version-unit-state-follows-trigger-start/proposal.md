---
version: 3
---

Issue: #1211.

## Why

**Persona: developer.** A Developer keeps unit B in sync with unit A. The Developer sets a follows trigger on B, applies A, and expects one plan of B per applied version, with no extra runs and no silent skip.

The user path this change must make work:

1. **API.** Create a follows trigger on unit B with source unit A and when applied through the API.
2. **CLI.** Apply unit A with the real OpenTofu CLI from a workstation or through a job.
3. **API.** Watch the plan job of unit B start once for the new final version and read the live log.
4. **Docs page.** Read the docs page that names which runs count as an apply and the saver read rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Event for a version older than the last final version of the unit. | The server answers 409 with code trigger.version-mismatch and starts nothing. Run `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` to see the trigger and its last error. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Job service refuses the plan for one follower. | The server records the error on that trigger and continues with the other followers. Fix the unit settings or pool, then fire the version again by hand. Run `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` to see the error. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Follows plan reads a source output without permission. | The server denies the read, writes an audit entry, and fails the reading step. Ask a namespace administrator to change the grants of the last saver. Run `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` to see the last saver. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |

Evidence for the claims above:

- The trigger dispatcher port and its conformance harness already exist. Proof: pkg/trigger/ports.go:136:// Outcome is the result of one trigger in a Dispatcher.Upstream call. and pkg/trigger/triggertest/dispatcher.go:76:out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
- The version-mismatch sentinel already exists in the trigger domain. Proof: pkg/trigger/trigger.go:48:ErrVersionMismatch = errors.New("state version is not a final state version of the unit") and pkg/trigger/dispatcher.go:129:return nil, ErrVersionMismatch
- Trigger rows already carry the last saver subject and stable principal ID. Proof: pkg/trigger/trigger.go:190:LastSaver string and pkg/trigger/trigger.go:192:LastSaverID string

## What Changes

- Dispatcher.Upstream verifies the final state version, enforces one plan per trigger and version, returns ErrVersionMismatch for an older version, and records per-trigger start errors.
- The OpenTofu final-version write sends the upstream event from publishUnitState, inside the fenced transaction. No observer port is added. Pulumi sends nothing in this slice.
- Follows plan jobs carry the trigger ID and check each source output read against the current grants of the last saver.
- Follows triggers publish a declared dependency edge for the graph and safe delete.
- Manual fire of a final version through the trigger fire route refires missed followers with the same idempotency key.
- A docs page names which runs count as an apply.

Out of scope:

- Run-time follower caps and run-time depth stop
- The when value outputs-changed and its changed-outputs filter
- Pulumi update sender; only the OpenTofu final-version sender ships here
- Schedule and review-request-push triggers

Depends on:

- write-state-version-unit-state-1
- job-service-unit-settings-pool
- output-sharing
- `write-state-version-unit-state-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A Developer who expects a follower plan after every run is surprised when a plan with no state change starts nothing; the docs page states that only a final state version counts.
- A refire after a missed event needs the version ID from unit history; the error answers name the trigger read route.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Dispatcher.Upstream is the trigger fan-out entry point. (`pkg/trigger/ports.go:192`)
- ErrVersionMismatch reports an upstream event that is not a final state version of the unit. (`pkg/trigger/trigger.go:48`)
- The conformance harness drives Upstream with an applied version. (`pkg/trigger/triggertest/dispatcher.go:76`)
- Trigger rows carry LastSaver and LastSaverID. (`pkg/trigger/trigger.go:190`)
- Upstream verifies the event against the final state versions of the unit. (`pkg/trigger/ports.go:183`)
- A test of the final-version read path exists. (`pkg/dustpan/triggers_versions_internal_test.go:36`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current), 11 (current), 12 (current), 13 (current), 14 (changed), 15 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "**A `follows` trigger names a source unit, and the server refuses a cycle at save.** After the source applies, a plan of the unit starts. The source is a unit reference, so a unit of one tool can follow a unit of another"
- Quote: "A `follows` trigger is a declared dependency edge. ADR 153 decision 2 says that a feature that declares a link between units adds its own kind of edge, with the evidence `declared`. The `follows` trigger does that. Such"
- Quote: "A `follows` trigger has a field `when` (question 2). The default is `applied`: after each apply of the source. The value `outputs-changed` comes in a later slice."
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else. No trigger starts an apply in these slices (RFC"
- Effect: This change implements the applied start, the declared edge, the sender filter, the plan-only start, the saver read rule and the trigger source kind.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets` for a secret output."
- Effect: The read of source outputs by a follows plan uses the last saver check with unit:read-outputs and unit:read-secrets.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change."
- Effect: The plan starts as the namespace service principal with only the stored plan-start grant; output reads still use the last saver.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The change ships the docs page in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What counts as an apply of the source unit? ADR 267 decision 12: for OpenTofu any final state version counts.
- When does a follows trigger fire? ADR 267 decision 13: field when, default applied; outputs-changed is out of scope.
- Whose grants govern source output reads? ADR 267 decision 14, ADR 273 decision 5 and ADR 338 decision 3: the last saver, fail closed.
- How does fan-out stay safe without a shared transaction? A per-trigger idempotency key per version replays the first job after a crash.
- Where does the sender hook in? In publishUnitState inside the fenced transaction; no observer port.
- Does a follows trigger cross namespaces? ADR 267 decision 21: no.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
