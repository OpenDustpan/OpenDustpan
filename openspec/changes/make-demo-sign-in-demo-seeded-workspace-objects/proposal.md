---
version: 1
---

## Problem

A developer tries a fresh server and needs one workspace with state, view, approval default, trigger, and sink to explore without manual setup.

## Evidence

- Main seeds the demo person on dev start. Proof: cmd/opendustpan/devdemo.go:40 (func devRun.seedDemo)
- Main has a demo person seed service with a test. Proof: pkg/principal/demoperson_test.go:13
- Main stores saved views. Proof: pkg/store/postgres/savedviews.go:23 (func Store.SavedViews)
- Main reads and writes the namespace approval default. Proof: pkg/gate/config.go:145 (func ConfigService.PutApprovalDefault)
- No ADR in the pack governs this change. Proof: pack adrs list is empty

## Fix boundary

- Seed one workspace with one state version for demo-admin on dev start.
- Seed one saved view, one approval default, one trigger, and one sink for the demo workspace.
- Make reseed idempotent: keep each existing demo object unchanged.

Out of scope:

- Sign-in and token flow for demo-admin
- Other demo tools, namespaces, and units

## Check

- Add failing smoke test for seeded workspace, state version, view, approval default, trigger, and sink in NEW `cmd/opendustpan/devdemoseed_test.go`. Check: NEW `cmd/opendustpan/devdemoseed_test.go`: TestSeedDemoWorkspaceObjects fails on main, passes after the change
- Add failing reseed test that runs seed twice plus a partial seed and keeps objects unchanged except missing ones in NEW `cmd/opendustpan/devdemoreseed_test.go`. Check: NEW `cmd/opendustpan/devdemoreseed_test.go`: TestSeedDemoWorkspaceReseed fails on main, passes after the change
- Extend demo seed with one workspace and its linked objects by stable ID with local disabled sink and trigger. Check: NEW `cmd/opendustpan/devdemoseed.go`: run `opendustpan dev` twice against the same database and list the demo workspace objects through `GET /api/dustpan/v1/namespaces/{}/views/{view_id}`
- Run wiring, docs page, and usability check from the docs page alone in NEW `docs/guides/demo.md`. Confirmed by: CI (`make check`) Check: Docs page NEW `docs/guides/demo.md` lists the `opendustpan dev` steps and seeded objects, Confirmed by: CI (`make check`)

Depends on:

- make-demo-sign-in-demo-admin-signin
- `make-demo-sign-in-demo-admin-signin` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Seeds demo data on dev run (`cmd/opendustpan/devdemo.go:40 (func devRun.seedDemo)`)
- Seeds the demo person (`pkg/principal/service.go:737 (func Service.SeedDemoPerson)`)
- Seeds the demo password (`pkg/store/internal/localuserstore/localuserstore.go:578 (func SeedDemoPassword)`)
- Writes the demo sign-in output (`cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn)`)
- Stores saved views (`pkg/store/postgres/savedviews.go:23 (func Store.SavedViews)`)
- Reads and writes the namespace approval default (`pkg/gate/config.go:145 (func ConfigService.PutApprovalDefault)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does seeding workspace objects change sign-in or token flow? No. Part 2 seeds data through existing domain services after demo-admin exists. Sign-in and token flow stay in part 1. The pack lists no ADR, so no ADR answers it.
- Does any ADR govern demo seeding, approval defaults, triggers, or sinks? No ADR in the pack covers demo seeding. The pack adrs list is empty and the roadmap limits part 2 to one workspace plus view, approval default, trigger, and sink with idempotent reseed. Sinks and triggers use loopback or disabled targets only, so no outbound call and no secret is seeded.
- What does reseed do when only some demo objects exist? Reseed matches each object by stable ID and repairs a partial seed by creating only the missing objects. A renamed or removed object is treated as missing and recreated under its demo stable ID. This answers the partial-seed and rename case.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
