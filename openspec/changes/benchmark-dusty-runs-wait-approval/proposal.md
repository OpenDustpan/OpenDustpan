Issue: #938.

## Why

**Persona: operator.** An Operator hosts Dusty runs that stop and wait for a person to confirm. The Operator needs measured data on how to hold a waiting run without wasting model tokens, holding server memory, or losing the run on restart, so the owner can decide the design.

The user path this change must make work:

1. **Docs page.** Read the benchmark note at docs/explanation/dusty-wait-benchmark.md and compare suspend and resume, poll, and end and restart on tokens, memory, resume time, scale, and restart behavior.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Benchmark note link in the docs fails the link check | The docs check reports a broken link to the benchmark note. | `make docs-lint` |

Evidence for the claims above:

- Durable scheduler wait behavior has a test on main. Proof: pkg/durable/scheduler_test.go:175
- Restart of schedulers has a test on main. Proof: pkg/durable/scheduler_test.go:266
- Agent window acceptance has a test on main. Proof: internal/agent/window_test.go:18
- Agent loop code lives under internal/agent in the pack. Proof: internal/agent

## What Changes

- Run a fixed benchmark of three simulated hold patterns for a Dusty run that waits for a person: suspend and resume, poll, and end and restart.
- Record per pattern: model token cost, server memory during the wait, time to resume after approval, scale with concurrent waiting runs, and behavior across a server restart, cancel, limits, and credential expiry.
- Write a benchmark note with method, numbers, and one recommended hold pattern.

Out of scope:

- Design of agent principals and delegations (RFC 121).
- Dusty run routes, tools, and MCP endpoint.
- Approval rules and confirmation steps.
- Default model selection (issue 939).

## Usability

Where the path still costs the person time or surprises them:

- The note still costs the reader time if method and raw numbers are not in one table per pattern.
- The recommendation surprises if it does not name the restart, scale, and credential failure modes it accepts.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that the scheduler waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main tests that a restart of many schedulers gives one catch-up. (`pkg/durable/scheduler_test.go:266 (TestARestartOfManySchedulersGivesOneCatchUp)`)
- Main tests window acceptance validation. (`internal/agent/window_test.go:18 (TestAcceptanceValidate)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 120: Dusty, the built-in agent, runs on durable operations** (not owner-tagged).

- Decisions that apply: 8 (current), 10 (current), 11 (current), 12 (current), 20 (changed), 21 (current).
- Changed by: ADR 128 changes decision 22; ADR 129 changes decisions 3, 7 and 20; ADR 129 extends; ADR 210 changes; ADR 297 changes decisions 13 and 14; ADR 311 extends; ADR 314 extends; ADR 354 changes decisions 13, 14, 15 and 16. Chain to the end: ADR 128, ADR 129, ADR 210, ADR 297, ADR 311, ADR 314, ADR 354, ADR 190, ADR 222, ADR 227, ADR 286, ADR 247, ADR 301, ADR 333, ADR 357, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 320, ADR 339, ADR 328, ADR 359, ADR 332.
- Current rule: this ADR, with ADR 128, 129, 210, 297, 311, 314, 354.
- Quote: "A run is a chain of turn operations. A turn is one model call and the tool calls of its answer. Each turn is one durable operation of the kind `agent.turn` (ADR 72 decision 1). A durable kind has a fixed list of steps, a"
- Quote: "A restart continues the run. A turn whose instance stops keeps its claim until the lease expires. Then another instance claims it and continues from the last recorded step (ADR 72 decision 2). A run holds no claim betwee"
- Quote: "Effects happen once. A step that proposes or cancels a change passes a key that it makes from the run, the turn and the index of the call. The job model gives one change for one key. So a turn that runs again after a tak"
- Quote: "Reads and model calls can repeat. A read tool is safe to run again. A model call that ran before a lost claim can run again on the next claim, and costs its tokens again. The token ledger records each attempt (decision 2"
- Quote: "Tokens are recorded for each attempt. Each model call writes one row to a token ledger, with the run, the turn, the token of the claim, the model name and four counts: input, output, cache read and cache write. The row n"
- Quote: "The cost of a run is measurable. A run answers two sums: the tokens of its recorded turns, and the ledger. The usage route answers the ledger for a time range, grouped by person, namespace or model. The metrics `dustpan_"
- Effect: The benchmark observes the durable turn model, the restart rule, the effects-once rule, the repeat-cost rule, and the ledger and cost rules. It changes no behavior and adds no wait state to the server.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Leases and durable operations across the gap. - The lease stays at 30 seconds (ADR 280 decision 8). - A gap shorter than the lease: the renewals fail, the step continues, and the next renewal after the gap extends the claim. - A gap longer"
- Effect: The benchmark observes lease and durable behavior across a short database gap and a longer gap. It states only what was observed and claims no exact server rule.
- Conflict: none.

**ADR 279: No release before the exit tests of milestones 1 to 7 pass** (owner-tagged (all decisions)).

- Decisions that apply: 1 (extended).
- Changed by: ADR 280 extends decision 1. Chain to the end: ADR 280, ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337.
- Current rule: this ADR, with ADR 280.
- Quote: "No release until milestones 1 to 7 pass. The project makes no release and pushes no `v*` tag until the exit test of each of milestones 1 to 7 in `docs/roadmap.md` passes. Milestone 7 is the milestone of ADR 280. (owner)"
- Effect: Milestone 10 is outside the release gate, so the benchmark note ships without a release check.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How do suspend and resume, poll, and end and restart map to the Dusty run model? ADR 120 decision 8 says each turn is one durable operation of kind agent.turn and a run holds no claim between turns, so the three patterns are simulated holds between turns, not new server wait states.
- What does each pattern cost in model tokens, server memory, and time to resume? ADR 120 decisions 12, 20, and 21 say each model attempt writes a ledger row, so token cost comes from the ledger, memory from the server process, and resume time from approval to the next model call.
- What breaks when a wait spans a server restart? ADR 120 decision 10 and ADR 298 decision 11 say what a restart and a database gap do to a claim, so the benchmark observes gaps, restarts at each step, and takeover, and states only what was observed.
- How do other agent products hold a run that waits for approval? The note surveys two outside agent products and records how each maps to suspend, poll, or end and restart.
- How are approval waits kept clear of credential expiry, run limits, and cancel? ADR 120 decisions 14, 16 and 19 end a run on credential loss, bound time, tokens and turns, and record a cancel request. The main numbers use a short fixed wait below all limits. Expiry, revocation, limit and cancel are separate rows.
- Where does the benchmark harness live and does it add a tool or dependency? The harness is an uncommitted local script that drives the existing dev server and reads the existing token ledger. No tool and no dependency is added.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
