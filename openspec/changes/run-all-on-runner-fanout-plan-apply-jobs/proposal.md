Roadmap: `docs/roadmap.md:717` (Fan out one plan and one apply job per unit under one change order.).

## Why

**Persona: runner operator.** A Runner operator runs a runner pool that executes jobs for many workspaces. Today one terragrunt run-all request has no server fan-out into one job per workspace, so the operator cannot review once and track each workspace result. This change adds the fan-out under one change order.

The user path this change must make work:

1. **API.** Operator creates one change order for the discovered workspaces from the guide and starts the plan phase.
2. **API.** Operator reviews the plan jobs and approves, then requests apply for the approved revision.
3. **E2e.** Runner claims run one plan job and one apply job per workspace, each with its own lock, state version, and history entry.
4. **Docs page.** Operator reads the guide for the fan-out record and the gated apply.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| One discovered workspace name does not exist in the namespace. | The server answers 404 with code `unit.not-found` and starts no job for that name. Run the unit list command to check the names. | `GET /api/dustpan/v1/namespaces/{}/units` |
| The stored plan of an apply job differs from the stored plan of its unit. | The server refuses the dispatch with the NEW error code `changeorder.plan-mismatch` and starts no apply. Run the unit list command to check the units of the revision. | `GET /api/dustpan/v1/namespaces/{}/units` |

Evidence for the claims above:

- Main has a runner domain package. Proof: pkg/runner
- Main has the runner protocol route. Proof: ANY /runner/v1
- Main has unit list and unit scope routes. Proof: GET /api/dustpan/v1/namespaces/{}/units
- Main has a test that a runner runs an operation started with its claim. Proof: TestRunnerRunsAnOperationStartedWithItsClaim

## What Changes

- Start one plan job with operation `plan` for each discovered workspace under one change order.
- Start one apply job with operation `apply` for each workspace whose plan has a change, after the apply request of a person.
- Bind each job claim to the stable unit ID of exactly one workspace with its own lock, state version, and history entry.

Out of scope:

- Discovery of Terragrunt units, refusals, preview, and audit of the discovery job.
- Dependency ordered apply starts.
- Local run-all and run-all in team CI.
- TFE-compatible adapter logic for run-all.

Depends on:

- run-all-on-runner-discovery
- `run-all-on-runner-discovery` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator still tracks 6 jobs under one change order, which costs time in the console list.
- The reason text terragrunt run-all fan-out surprises an approver who did not run Terragrunt.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs an operation started with its runner claim. (`pkg/durable/runner_claimed_test.go:16 (func TestRunnerRunsAnOperationStartedWithItsClaim)`)
- Main keeps discovery during maintenance. (`pkg/dustpan/maintenance_test.go:257 (func TestMaintenanceKeepsDiscovery)`)
- Main ships every audit entry in order and moves the cursors. (`pkg/audit/ship_test.go:257 (func TestShipOnceCopiesEveryEntryInOrderAndMovesTheCursors)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 190: Change orders: an object above jobs** (owner-tagged: decision 5).

- Decisions that apply: 3 (current), 5 (current), 9 (changed).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 299 changes decisions 4, 9, 10, 12 and 14; ADR 300 changes decision 10; ADR 344 changes decision 14; ADR 365 changes decision 10. Chain to the end: ADR 222, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 286, ADR 335, ADR 351, ADR 354, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 222, 225, 299, 300, 344, 365.
- Quote: "Each unit of the change order is an `identity.Object` unit reference with its stable unit ID set"
- Quote: "Jobs run under a change order."
- Quote: "An apply job applies the plan of its plan job."
- Effect: The change uses the existing change order plan and apply flow to start one plan job per unit and one apply job per changed unit, with each unit bound by stable unit ID and each apply bound to the stored plan of the same unit.
- Conflict: none.

**ADR 196: Change order ports, errors, fences and conformance suites** (not owner-tagged).

- Decisions that apply: 5 (changed), 7 (current).
- Changed by: ADR 222 changes decisions 1 and 5; ADR 230 changes decisions 1 and 5. Chain to the end: ADR 222, ADR 230, ADR 286, ADR 335, ADR 351, ADR 354, ADR 268, ADR 355.
- Current rule: this ADR, with ADR 222, 230.
- Quote: "StartPlan(ctx context.Context, a Actor, u UnitPlan, key string) (JobRef, error)"
- Quote: "Start the plan jobs, then cancel the plan jobs of the revision before."
- Effect: The fan-out starts jobs only through the change order jobs port and the durable operations changeorder.plan and apply, with retries under the existing start keys.
- Conflict: none.

**ADR 365: Wording refinements edit an ADR in place, with a changelog (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "It now says "for each unit whose plan has a change"."
- Effect: The change starts an apply job only for each unit whose plan has a change and starts no apply job for a unit with no change.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How many plan jobs and apply jobs does one change order start? ADR 190 decisions 5 and 9 answer it: one plan job per unit of each revision and one apply job per changed unit that names the stored plan of the same unit. No new fan-out route is used.
- How does each job bind to exactly one workspace? ADR 190 decision 3 answers it: each unit is a unit reference with tool, namespace, tool name and stable unit ID, and the core never parses the tool name.
- Does a unit with no change get an apply job? ADR 365 decision 4 answers it: for each unit whose plan has a change, so a unit with no change starts no apply job.
- What gates apply starts and what stops sibling jobs? ADR 190 decision 10 and ADR 196 decision 7 answer it: per-unit apply grants and the existing plan and apply operations govern the fan-out, including stop behavior and revision cancel.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
