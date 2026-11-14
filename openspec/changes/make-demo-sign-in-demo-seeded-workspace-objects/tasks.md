## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing smoke test for seeded workspace, state version, view, approval default, trigger, and sink in NEW `cmd/opendustpan/devdemoseed_test.go`. Check: NEW `cmd/opendustpan/devdemoseed_test.go`: TestSeedDemoWorkspaceObjects fails on main, passes after the change
- [ ] 1.2 Add failing reseed test that runs seed twice plus a partial seed and keeps objects unchanged except missing ones in NEW `cmd/opendustpan/devdemoreseed_test.go`. Check: NEW `cmd/opendustpan/devdemoreseed_test.go`: TestSeedDemoWorkspaceReseed fails on main, passes after the change

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Extend demo seed with one workspace and its linked objects by stable ID with local disabled sink and trigger. Check: NEW `cmd/opendustpan/devdemoseed.go`: run `opendustpan dev` twice against the same database and list the demo workspace objects through `GET /api/dustpan/v1/namespaces/{}/views/{view_id}`
- [ ] 3.2 Run wiring, docs page, and usability check from the docs page alone in NEW `docs/guides/demo.md`. Confirmed by: CI (`make check`) Check: Docs page NEW `docs/guides/demo.md` lists the `opendustpan dev` steps and seeded objects, Confirmed by: CI (`make check`)
