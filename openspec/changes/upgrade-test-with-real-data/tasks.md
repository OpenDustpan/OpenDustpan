## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the failing full fixture test for SQLite Check: NEW `TestUpgradeRealDataSqlite` fails on main
- [ ] 1.2 Add the failing full fixture test for PostgreSQL Check: NEW `TestUpgradeRealDataPostgres` fails on main

## 2. Build

- [ ] 2.1 Build the fixture runner with `newUpgradeFixture` Check: `make upgrade-test` runs the fixture on both dialects with an access token on ANY /api/stacks/{}/{} and ANY /api/v2/workspaces/{}/current-state-version
- [ ] 2.2 Check old-binary refusal as a process exit Check: Old binary exits non-zero with a newer-schema log line; new binary read of ANY/GET /api/dustpan/v1/upgrade-check with an access token
- [ ] 2.3 Add the CI job Check: CI job runs `make upgrade-test` once per supported old release
- [ ] 2.4 Set the NEW pin file `e2e/upgrade/pinned-commit.txt` after dependencies merge Check: Pin file set after agent-shapes and key-rotation merge and verified by read of ANY/GET /api/dustpan/v1/upgrade-check with an access token; old binary built with release-build target

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the e2e step against the real CLIs on ANY /api/stacks/{}/{} and ANY /api/v2/workspaces/{}/current-state-version with an access token Check: e2e step with real Pulumi CLI and OpenTofu on ANY /api/stacks/{}/{} and ANY /api/v2/workspaces/{}/current-state-version with an access token verifies the upgraded database
- [ ] 4.2 Write the operator upgrade docs page with backup, stop, start and check steps and run the usability check Check: Docs commands run as written in CI; `make check` with zero findings; usability check of the upgrade page
