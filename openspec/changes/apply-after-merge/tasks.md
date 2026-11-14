## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service test for merge event to apply jobs with pinned branch and sha. Check: TestMergeStartsApplyAtMergeCommit fails on main.
- [ ] 1.2 Add failing service test that a pull request branch push starts no apply. Check: TestBranchPushStartsNoApply fails on main.
- [ ] 1.3 Add failing service test that a refused gate fails the merge apply with its decision record. Check: TestMergeApplyKeepsGates fails on main.
- [ ] 1.4 Add failing service test for replayed merge events, partial writes and merges with no touched units. Check: TestDuplicateMergeCreatesOneJob fails on main.
- [ ] 1.5 Add failing service test for changed replan, no reviewed plan and missing sha paths. Check: TestMergeApplyReplanDiffersResetsApprovals fails on main.
- [ ] 1.6 Add failing service test for a second merge on a unit with a running apply. Check: TestConcurrentMergesQueue fails on main.

## 2. Build

- [ ] 2.1 Build the merge starter with pinned branch and sha. Check: `go test ./internal/mergeapply/... -run TestMerge -count=1` passes.
- [ ] 2.2 Wire the starter to the job service with authorization actions. Check: `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` with a personal token returns the merge apply with its branch and commit sha.
- [ ] 2.3 Add e2e step with real binary over TLS: merge starts apply, branch push does not. Check: e2e step against the fake forge passes on SQLite and PostgreSQL with an existing runner: `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` with a personal token shows the merge apply, and a branch push starts no apply.
- [ ] 2.4 Write the docs page for apply after the merge. Check: Docs page runs as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check wiring and authorization actions. Check: Route declares its authorization actions and the route test passes.
- [ ] 4.2 Run e2e step against the real CLI path where visible. Check: e2e step with existing runner and fake forge passes: `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` with a service token shows the merge apply pinned to branch and sha.
- [ ] 4.3 Run usability check for errors and help commands. Check: Persona completes the path from the docs page alone.
- [ ] 4.4 Run full check suite. Check: `make check` reports zero findings.
