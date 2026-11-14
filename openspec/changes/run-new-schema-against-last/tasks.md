## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing e2e mixed-version cases for 2-tag, 1-tag, and 0-tag reports, tag choice, shallow-clone refusal, and build failure in e2e/upgrade_mixed_test.go:74 (func TestMixedVersion) and in the NEW test file e2e/upgrade_mixed_tags_test.go. Check: TestMixedVersion plus NEW TestMixedVersionTagDiscovery fail on main on the missing tag list, then pass after the change, through ANY /api/v2/state-versions and ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add failing SQLite sequential case with one server at a time on one file in the NEW test file e2e/upgrade_mixed_sqlite_test.go. Check: NEW SQLite sequential case through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token passes with one server at a time. Confirmed by: CI (the agent writes the e2e test).

## 2. Wiring

- [ ] 2.1 Extend the e2e mixed-version harness in e2e/upgrade.go to build the last 2 ancestor v* tags and run `pulumi up` and `tofu apply` against each old binary. Check: `go test -tags upgrade -run TestMixedVersion ./e2e` drives `pulumi up` and `tofu apply` against each old binary through ANY /api/v2/state-versions and ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token on SQLite
- [ ] 2.2 Add the NEW `make mixed-version-test` target in the NEW file Makefile (Makefile is not on main) for SQLite and PostgreSQL with DUSTPAN_TEST_POSTGRES_URL. Check: The NEW `make mixed-version-test` passes on SQLite and with DUSTPAN_TEST_POSTGRES_URL set, through ANY /api/v2/state-versions with an access token
- [ ] 2.3 Add the NEW CI job in the NEW file .github/workflows/ci.yml next to the upgrade test with tag fetch and PostgreSQL service. Check: CI log shows the matrix job next to the upgrade test with all tags fetched and the PostgreSQL service set for DUSTPAN_TEST_POSTGRES_URL. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run full check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL Check: `make check` with zero findings and PostgreSQL cases run against DUSTPAN_TEST_POSTGRES_URL. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Add docs for the matrix, the zero-tag report, and the refusal error Check: Docs page runs as written and the stale-write error names the one command. Confirmed by: CI (an e2e step in the NEW file e2e/run_new_schema_against_last_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Run usability check from the docs page alone Check: Operator completes the matrix from the docs page and each error names the one helping command. A person's step.
