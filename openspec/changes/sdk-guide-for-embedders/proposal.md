---
version: 2
---

## Why

**Persona: embedder.** An embedder builds an own Go server binary with the SDK. Today the entry point exists on main but no single guide shows the full path from NewServer through the store option and routes to a passing conformance run for an own adapter.

The user path this change must make work:

1. **Docs page.** Read the embed guide page and copy the minimal program with dustpan.NewServer and the NEW built-in store and blob constructors.
2. **SDK.** Start the program that mounts the routes as the guide shows.
3. **API.** Probe GET /api/dustpan/v1/capabilities with an access token to confirm the server answers.
4. **CLI.** Run the named pkg conformance suite against an own adapter to prove it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Guide example does not build | The build fails with the missing import. Add the pkg/dustpan import shown in the guide and rerun the NEW build check. | Run the NEW test `TestGuideExampleBuilds` with `go test ./pkg/dustpan -run TestGuideExampleBuilds -count=1` |
| Capabilities probe does not answer | The probe fails with the server error text. Start the NEW example server and rerun the probe with an access token for the NEW probe check. | Run the NEW test `TestGuideProbes` with `go test ./pkg/dustpan -run TestGuideProbes -count=1` |
| Own adapter fails a conformance check | The named suite fails and names the unmet method. Implement that port method and rerun the suite command from the guide for the NEW test TestGuideFailureNamesMethod. | `go test ./pkg/blob/blobtest -count=1` for the NEW test `TestGuideFailureNamesMethod` |

Evidence for the claims above:

- The SDK entry point exists on main as dustpan.NewServer. Proof: main lists pkg/dustpan/dustpan.go:774 (func NewServer) in package pkg/dustpan
- The binary wiring on main lives in a thin main package. Proof: main lists package cmd/opendustpan at cmd/opendustpan
- The tool-neutral API on main exposes installation routes the guide can probe. Proof: main lists ANY/GET /api/dustpan/v1/capabilities and ANY/GET /api/dustpan/v1/components from the route table or API description

## What Changes

- One SDK guide page for embedders with NewServer, the NEW built-in constructors, routes, and conformance runs
- NEW built-in constructors under pkg/dustpan for the embed path
- One tested NEW minimal example program wired like cmd/opendustpan

Out of scope:

- Release artifact build and publish
- Production install TLS and reverse proxy setup
- Upgrade test with real data
- Agent shapes
- Key-encryption key rotation

Depends on:

- docs-commands-run-in-tests
- `docs-commands-run-in-tests` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Embedders lose time when the example drifts; the example builds in CI.
- Embedders face surprise when internal wiring looks public; the guide names only constructors, ports, and suites.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main provides the SDK entry point constructor NewServer. (`pkg/dustpan/dustpan.go:774 (func NewServer)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 35: The SDK lives in pkg/, internal code in internal/** (not owner-tagged).

- Decisions that apply: 1 (changed), 3 (current).
- Changed by: ADR 272 changes decision 1. Chain to the end: ADR 272, ADR 364.
- Current rule: this ADR, with ADR 272.
- Quote: "`pkg/dustpan` is the SDK entry point: `dustpan.NewServer` takes functional options for every replaceable port."
- Quote: "`cmd/opendustpan` stays a thin main that wires the built-in adapters from flags and environment variables."
- Effect: The guide documents only the SDK entry named in this ADR and uses cmd/opendustpan only as reference wiring. Decision 1 is read as changed by ADR 272.
- Conflict: none.

**ADR 272: The public SDK surface** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (current), 4 (extended).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "An embedder gets the built-in adapters through these functions."
- Quote: "CI fails when the file is not up to date."
- Quote: "No exported signature in `pkg/` names a type from `internal/`."
- Effect: The change adds the NEW built-in constructors under pkg/dustpan and the guide names only those constructors, ports, and suites. Adapter types stay private.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- What does an embedder import and call to build a server with an own adapter? ADR 35 decision 1 as changed by ADR 272 decision 1 answers it: pkg/dustpan with NewServer and functional options, plus the NEW constructors; ports and suites stay in pkg.
- How does the guide state what is stable across releases? ADR 272 decisions 3 and 4 answer it: the checked-in snapshot defines what is stable; the guide never names internal paths.
- Which wiring example does the guide use? ADR 35 decision 3 answers it: cmd/opendustpan is the thin reference wiring.
- Which probe wiring does the guide promise? No probe path is promised. The guide proves the server with GET /api/dustpan/v1/capabilities with an access token, so no probe-path decision stays open.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
