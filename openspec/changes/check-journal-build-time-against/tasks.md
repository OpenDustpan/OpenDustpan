## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the e2e timing test in the NEW file e2e/journal_build_time_test.go that fails on main because no result is recorded Check: Route ANY /api/stacks/{}/{}/{}/update/{}/complete with an access token; `make e2e` fails on main. Confirmed by: CI (the agent writes the e2e test).

## 2. Measure

- [ ] 2.1 Run the real Pulumi CLI at the tuned largest accepted size with journal version forced on through e2e/journalcap.go and record 5 build times plus the CLI wait time seen on a stalled complete call Check: Route ANY /api/stacks/{}/{}/{}/update/{}/complete with an access token; `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Check the 5-minute bound value in pkg/stack/journal_bounds_test.go during the run Check: No route: the test reads the bound value only; `go test ./pkg/stack -run TestJournalBuildDurationDefaultIsFiveMinutes -count=1`

## 3. Definition of done

- [ ] 3.1 Write the margin in the NEW docs/reference/journal-build-time.md with the date, CLI version, backend, build times and wait time Check: No route: the test renders the docs file only; `make docs-lint`
- [ ] 3.2 Walk the docs page from the page alone and check each error names one command Check: No route: a person reads the docs page only; usability check recorded in the pull request. A person's step.
- [ ] 3.3 Run the full check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
