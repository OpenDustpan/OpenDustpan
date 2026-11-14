---
version: 4
---

## Why

**Persona: operator.** An operator keeps the fleet clean without manual scans. Today the schedule row and the daily state check exist only in design text, so stale left-behind and managed-twice marks linger. This change runs the worker on time, every time, with a clear audit trail.

The user path this change must make work:

1. **Server start.** Operator sets schedule defaults at server start, then checks sweep rows in the API.
2. **API.** Namespace administrator lists sweeps and starts a state check on demand from the API.
3. **Docs page.** Operator follows the docs page to verify tick interval, offset spread, and audit entries.
4. **Console.** Operator reviews state-check rows beside cloud sweeps in the console.
5. **E2e.** E2E drives a tick and a state check against the real store and asserts audit entries.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start of a sweep or state check while one runs for the same target or namespace. | 409 with the NEW error code `unmanaged.scan-busy`, body names the running sweep. Wait for it to end, then retry. | `GET /api/audit/entries?namespace={ns}` |
| Manual start of a state check without the grant. | 403 with the NEW error code `access.forbidden`, body names the missing `namespace:start-sweep` grant. Ask a namespace administrator for the grant. | `GET /api/server/namespaces/{}/grants` |

Evidence for the claims above:

- Main computes the spread daily offset for state checks. Proof: internal/unmanaged/scan.go:50 (func StateCheckOffset)
- Main has the durable schedule test that this change extends. Proof: pkg/durable/durabletest/durabletest_test.go:35 (TestMemSchedules)
- Main checks its server routes through a route check helper. Proof: internal/api/server/routes.go:222 (func Handler.checkRoutes)

## What Changes

- 15-minute schedule worker that starts due sweeps and daily state checks at StateCheckOffset.
- State check run from the resource index with contested and finding key classification as a sweep of kind state.
- Busy guard with the NEW error code `unmanaged.scan-busy`, fence checks, and audit entries.
- NEW `POST /api/dustpan/v1/namespaces/{namespace}/state-checks` route with `namespace:start-sweep`.
- Docs page and usability check for schedule and state check.

Out of scope:

- Cloud sweep runner claims, report upload, and finding triage from earlier parts.
- Context read and write API and focus view from earlier parts.
- Sweep target create, change, and delete lifecycle from earlier parts.

Depends on:

- `160-165-report-intake-findings` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator sees last run and result counts beside cloud sweeps in existing sweep rows.
- A refused double start names the running sweep ID and its start time.
- Docs state plainly that a state check never reads cloud APIs and never resolves a purged-unit finding alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main computes the spread daily offset for state checks. (`internal/unmanaged/scan.go:50 (func StateCheckOffset)`)
- Main stores durable schedules and has an in-memory schedule test. (`pkg/durable/durabletest/durabletest_test.go:35 (TestMemSchedules)`)
- Main checks its server routes through a route check helper. (`internal/api/server/routes.go:222 (func Handler.checkRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 163: The resource index and unmanaged resources found from state** (not owner-tagged).

- Decisions that apply: 10 (current), 11 (changed).
- Changed by: ADR 166 changes decisions 5 and 11; ADR 210 changes; ADR 216 changes decision 9. Chain to the end: ADR 166, ADR 210, ADR 216, ADR 286, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 166, 210, 216.
- Quote: "Left behind and managed twice need no sweep. A **state check** reads the index of one namespace and classifies two sets of keys:"
- Quote: "When a state check runs. One time each day for each namespace with at least one unit, from the schedule row of ADR 164 decision 16."
- Effect: This change builds the daily state check of decision 11 that reads the index as decision 10 defines, as a sweep of kind state in the records of ADR 164.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 14 (changed), 16 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "The schedule. A schedule row `unmanaged.schedule` of ADR 72 runs every 15 minutes. It starts a sweep of each enabled target whose time came, and the daily state check of each namespace at the tick of its offset (ADR 163 decision 11). It a"
- Quote: "The steps of a cloud sweep: 1. **Start.** A person with `namespace:start-sweep`, or the schedule, starts a sweep of a target. The store refuses a second sweep of the same target that did not end (`ErrScanBusy`). 2. **Sweep.** A runner of"
- Effect: This change builds the 15-minute schedule tick of decision 16 and the single busy guard ErrScanBusy of decision 14 for sweeps and state checks.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How often does the schedule run and what does it start? ADR 164 decision 16 answers: one schedule row runs every 15 minutes and starts due sweeps and the daily state check at its offset.
- When does a state check run? ADR 163 decision 11 answers: once per day per namespace with at least one unit, plus after soft delete, restore, purge, and on request as a sweep of kind state.
- What does a state check read? ADR 163 decision 10 answers: contested keys from Index.Contested plus the key of each open finding, classified from the index alone.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
