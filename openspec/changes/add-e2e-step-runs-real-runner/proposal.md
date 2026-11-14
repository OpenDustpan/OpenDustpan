---
version: 2
---

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
| Caller without the pool read grant lists pools. | 403 with the runner forbidden code. Run `make e2e` to show the missing grant. | `make e2e` |
| Join with a revoked or unknown join token fails. | 404 with the no-such-token code. Run `make e2e` to show the join refusal. | `make e2e` |
| Retire of a runner that still holds a claim fails. | 409 with the runner-busy code. Run `make e2e` to show the open claim. | `make e2e` |

Evidence for the claims above:

- Runner management routes live under the old prefix on main. Proof: internal/api/runnerapi/routes.go:76:	poolsPath       = "/api/runners/{namespace}/pools"
- Drain and retire are sub-paths on main. Proof: internal/api/runnerapi/routes.go:81:	drainPath       = "/api/runners/{namespace}/runners/{runner}/drain"
- No ServedRoutes symbol exists in pkg, internal or cmd outside tests. Proof: rg -n -F --glob '!*_test.go' 'e2e.ServedRoutes' pkg internal cmd matches 0

## What Changes

- Drive every runner management route and every runner protocol step in e2e with a pinned real runner and a local launcher in the sandboxed process.
- Cover plan, apply, cancel, drain and retire with no retry mask, with refusals, claim expiry, drain race, Idempotency-Key replay on drain and retire, both stores over TLS and cleanup.
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Runner management pool, join-token, runner, drain and retire handlers are declared in the runner API route file. (`internal/api/runnerapi/routes.go:76`)
- Drain handler path is declared in the runner API route file. (`internal/api/runnerapi/routes.go:81`)
- ServedRoutes function defines the covered route list. (`e2e/coverage.go:42`)
- Retired runner refusal has a hostile test. (`internal/api/runnerapi/refusal_test.go:261`)
- Runner management in maintenance mode has a test. (`pkg/dustpan/runners_maintenance_test.go:102`)

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
- Effect: This change drives the moved management routes and the runner protocol with a real runner. It moves no path. The protocol stays outside the versioned prefix and drain and retire stay POST sub-paths. Mutating tool-neutral calls send Idempotency-Key.
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
- Quote: "Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: The change writes no migration and edits no baseline because it adds only e2e cover.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this part move the pool and runner routes? No. The split scope states this part does not change routes. The route move ships in the pool-route-move dependency under ADR 210 decisions 1, 3 and 5. This part only drives the moved routes.
- Does the runner protocol move too? No. ADR 210 decision 2 keeps a protocol endpoint that a runner stores outside the versioned prefix. The e2e flow drives the protocol where it is.
- Is an alias for the old prefix needed? ADR 73 decision 4 answers it: no shim, alias, second read path or conversion for a shape that no tag shipped.
- Do drain, retire and join-token calls need Idempotency-Key? Yes for tool-neutral mutating routes only. ADR 210 decision 6 requires Idempotency-Key on each mutating tool-neutral route. The flow sends the header on drain and retire and checks the replay answer. Join with a token uses the runner protocol outside the versioned prefix, so the header does not apply there.
- Does the pinned runner count as a new dependency that needs the owner? No new dependency. The pin names the released runner artifact that the e2e suite already downloads. No package, module or phone-home destination is added.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
