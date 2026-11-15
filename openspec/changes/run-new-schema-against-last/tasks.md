## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Extend in place the existing mixed-version test e2e/upgrade_mixed_test.go:74 (func TestMixedVersion) with the 2-tag, 1-tag and 0-tag reports, the tag choice, the shallow-clone refusal and the build failure; put only the new tag-discovery cases in the NEW test file e2e/upgrade_mixed_tags_test.go (TestMixedVersionTagDiscovery). Check: `go test -tags upgrade -run 'TestMixedVersion' ./e2e` fails on main on the missing tag list, then passes, through ANY /api/v2/state-versions with an access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add the SQLite sequential case, one server at a time on one file, in the NEW test file e2e/upgrade_mixed_sqlite_test.go (new behavior). Check: the case passes through ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token. Confirmed by: CI (the agent writes the e2e test).

## 2. Wiring

- [ ] 2.1 Extend in place the existing mixed-version harness in e2e/upgrade.go to build the last 2 ancestor v* tags and run `pulumi up` and `tofu apply` against each old binary. Check: `go test -tags upgrade -run TestMixedVersion ./e2e` drives `pulumi up` and `tofu apply` against each old binary through ANY /api/v2/state-versions and ANY /api/stacks/{}/{}/{}/state/{}/outputs with an access token on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Extend in place the existing target `mixed-version-test` of the Makefile (Makefile:310) for SQLite and for PostgreSQL with DUSTPAN_TEST_POSTGRES_URL. Check: `make mixed-version-test` passes on SQLite and with DUSTPAN_TEST_POSTGRES_URL set.
- [x] 2.3 Extend in place the existing step "Mixed-version test" of the CI job in .github/workflows/ci.yml (:638-673, next to the step "Upgrade test") with the tag fetch and the PostgreSQL service. Check: the CI log of the step shows all tags fetched and DUSTPAN_TEST_POSTGRES_URL set. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step (after the workflow patch is applied).

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run full check with PostgreSQL cases against DUSTPAN_TEST_POSTGRES_URL Check: `make check` with zero findings and PostgreSQL cases run against DUSTPAN_TEST_POSTGRES_URL. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 No route: the test checks the page text and the commands it names only. Extend the existing guide docs/guides/backup-restore-upgrade.md with the matrix, the zero-tag report and the refusal error. Check: an e2e step in the NEW file e2e/run_new_schema_against_last_docs_test.go checks the page text (the matrix, the zero-tag report and the refusal error that names its one command) and that the Makefile recipe the page names (`make mixed-version-test`) exists, as e2e/install_docs_test.go does for the install page; the section holds contributor commands and no route, so nothing runs against the e2e test server. `make docs-lint` passes on each edited guide. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run usability check from the docs page alone Check: an Operator completes the matrix from docs/guides/backup-restore-upgrade.md alone and each error names the one helping command. A person's step.
