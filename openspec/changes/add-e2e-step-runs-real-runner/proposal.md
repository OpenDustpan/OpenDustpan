Issue: #1037.

## Why

**Persona: runner operator.** A runner operator sets up pools and runners and must trust that each management route works with a real runner. The operator today cannot see that proof because no e2e step drives the routes with a real runner.

The user path this change must make work:

1. **API.** Create a runner pool and mint a join token through the tool-neutral API.
2. **E2e.** Join a pinned real runner and run plan and apply jobs with a local launcher in a sandboxed process.
3. **Docs page.** Cancel one job, drain the runner, retire it, and read each answer and the docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller without the pool read grant lists pools. | 403 with code runner.forbidden and a message that names the missing read action. | `opendustpan doctor` |
| Join with a revoked or unknown join token fails. | 404 with code runner.no-such-token and a message that names the join command. | `opendustpan doctor` |
| Retire of a runner that still holds a claim fails. | 409 with code runner.runner-busy and a message that names the drain command. | `opendustpan status` |

Evidence for the claims above:

- Runner management routes live under the old prefix on main. Proof: internal/api/runnerapi/routes.go:76: poolsPath = "/api/runners/{namespace}/pools"
- Drain and retire are sub-paths on main. Proof: internal/api/runnerapi/routes.go:81: drainPath = "/api/runners/{namespace}/runners/{runner}/drain"
- No e2e ServedRoutes list exists in pkg, internal or cmd outside tests. Proof: rg -n -F --glob '!*_test.go' 'e2e.ServedRoutes' pkg internal cmd matches 0

## What Changes

- Drive every runner management route and every runner protocol step in e2e with a pinned real runner and a local launcher in the sandboxed process.
- Cover plan, apply, cancel, drain and retire with no retry mask, with refusals, claim expiry, drain race, Idempotency-Key replay, both stores over TLS and cleanup.
- Add the operator docs page for pool setup, join, drain and retire.

Out of scope:

- Runner management path names and route behavior
- Launcher run and helper modes
- Job cancel semantics beyond driving the cancel route
- Console run page

Depends on:

- add-e2e-step-runs-real-runner-pool-route-move
- add-e2e-step-runs-real-launcher-binary-modes
- add-job-session-paths-runner-2
- add-job-service-routes-start-1
- `add-e2e-step-runs-real-launcher-binary-modes` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator still repeats pool, token and runner id across four calls; one worked example with copyable commands lowers that cost.
- Drain and retire errors must name the one command that shows the open claim, or the operator guesses.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 1 (current), 2 (current), 5 (current), 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Each route of the tool-neutral REST API moves under `/api/dustpan/v1/`."
- Quote: "A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Quote: "An operation that is not a create, a read, an update or a delete is a `POST` on a sub-path of its resource"
- Quote: "Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Effect: This change drives the management routes and the runner protocol with a real runner. It moves no path. The protocol stays outside the versioned prefix and drain and retire stay POST sub-paths. Mutating calls send Idempotency-Key.
- Conflict: none.

**ADR 73: Until 1.0, a change can break everything except the tool-compatible APIs** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (extended).
- Changed by: ADR 322 changes decision 6; ADR 322 extends decision 4. Chain to the end: ADR 322, ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 322.
- Quote: "Until 1.0, a change can break what is ours."
- Quote: "No compatibility code for unreleased shapes. Do not write a shim, an alias, a second read path or a data conversion for a shape that no tag shipped."
- Effect: The change adds e2e cover only. It writes no shim and no alias.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (changed), 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place."
- Quote: "No migration or compatibility code for untagged shapes (owner). Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: The change writes no migration and edits no baseline because it adds only e2e cover.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The error table in proposal.md names the command 'opendustpan pool get --help', but the open questions record that the 'opendustpan pool' command does not exist. The one command that helps is unverified. Resolution: answered: Errors now use an existing API call and opendustpan doctor, both verified against the command list and the route table.
- R1.2 (open question): The 'Open questions' section fails the code checks: the drain route is not in the API description or route table, and the spec still names '/api/runners'. The spec-ready gate is not met, so the change cannot be built. Resolution: answered: Spec scenarios and error commands now name only routes present in the route table. Open questions reads none because no ADR leaves a question open.
- R1.3 (scope): The roadmap item says this part 'does not change routes', but tasks 2.1 and the proposal move the pool, join-token, runner, drain and retire routes to the new prefix. The route move belongs to the earlier split part (add-e2e-step-runs-real-runner-pool-route-move). Resolution: answered: Route move text removed from what_changes and tasks. The move lives in the pool-route-move dependency. This change only drives the routes.
- R1.4 (adr conflict): ADR 210 decision 6 (owner decision 2026-10-18) quote: 'Each route of the tool-neutral API that changes data (POST, PUT, PATCH, DELETE) accepts the header Idempotency-Key.' The new POST routes for join-tokens, drain and retire have no Idempotency-Key requirement or scenario. Resolution: answered: Added an Idempotency-Key requirement with a replay scenario for mutating runner calls, per ADR 210 decision 6.
- R1.5 (missing scenario): The proposal error table lists 403 runner.forbidden, 404 runner.no-such-pool for a revoked or unknown join token, and 409 runner.runner-busy on retire. The spec has no scenario for any of these refusals. Resolution: answered: Added a refusals requirement with 403, 404 and 409 scenarios that match the error table.
- R1.6 (failure mode): A runner that crashes or loses its network while it holds a job claim is not covered: no requirement for claim expiry, re-claim or job failure. The no-retry-mask flow would hide or fail such a case without a defined result. Resolution: answered: Added a claim-expiry requirement: a crashed runner loses its claim at lease expiry and another runner takes the job.
- R1.7 (missing scenario): CLAUDE rules require each endpoint to declare its authorization actions and objects. The spec gives no scenario that names the action per route (for example the read action on list, the manage action on drain), though tasks 2.2 says to wire them. Resolution: answered: Added an authorization requirement that names the read action on lists and the manage action on drain and retire, with refusal scenarios.
- R1.8 (failure mode): A drain request that races with a new claim on the same runner is not covered. The scenario says the drained runner takes no new claim, but no concurrent case or ordering rule is specified. Resolution: answered: Added a drain-race scenario: a claim that starts before drain keeps its lease while the drained runner takes no new claim.
- R1.9 (open question): The pinned real runner version is a new external artifact. The design names drift as a risk, but no decision says how the pin is set, who approves it, or whether it counts as a dependency that needs the owner (project.md). Resolution: answered: Considered answers that the pin names a released e2e artifact and adds no dependency, so no owner decision is needed.
- R1.10 (scope): Job cancel semantics are out of scope, yet the e2e scenario asserts that a canceled job stops. The change depends on the job service routes, which are not yet present, so the scenario is not testable as written. Resolution: answered: Out of scope now states cancel semantics belong to the job service dependency. The spec only drives the cancel route and checks that the job stops.
- R1.11 (missing scenario): tasks 3.1 requires the flow on SQLite and PostgreSQL over TLS, but no spec scenario names the dialect or TLS path. The failing-test mapping for those variants is missing. Resolution: answered: Added a matrix requirement that runs the same flow on SQLite and on PostgreSQL over TLS against the real binary.
- R1.12 (failure mode): The design names the risk that a failed flow leaves a runner or pool that blocks later runs. No cleanup or teardown requirement exists, so a failed run can break the next run. Resolution: answered: Added a cleanup requirement that removes the pool and the runner after the flow, even after a failure.

## Open questions

none

Questions considered and answered:

- Does this part move the pool and runner routes? No. The split scope states this part does not change routes. The route move ships in the pool-route-move dependency under ADR 210 decisions 1, 3 and 5. This part only drives the routes.
- Does the runner protocol move too? No. ADR 210 decision 2 keeps a protocol endpoint that a runner stores outside the versioned prefix. The e2e flow drives the protocol where it is.
- Is an alias for the old prefix needed? ADR 73 decision 4 answers it: no shim, alias, second read path or conversion for a shape that no tag shipped.
- Do drain, retire and join-token calls need Idempotency-Key? Yes. ADR 210 decision 6 requires Idempotency-Key on each mutating tool-neutral route. The e2e flow sends the header on join-token, drain and retire calls and checks the replay answer.
- Does the pinned runner count as a new dependency that needs the owner? No new dependency. The pin names a released runner artifact that the suite already downloads for e2e. No package, module or phone-home destination is added, so no owner approval is needed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
