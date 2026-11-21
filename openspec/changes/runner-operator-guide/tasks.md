## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing test first

- [ ] 1.1 Add a docs test in the NEW file `docs/runner_guide_test.go` that runs each command of the runner operator page as written. Check: TestDocsRunnerGuideCommands fails on main before the page exists and passes after, covering scenarios expired-token-renew, wrong-scope-no-claims, drain-lets-claim-finish, secret-not-in-shell-history, token-used-help, not-found-help, retired-help.

## 2. Guide content

- [ ] 2.1 Write the NEW file `docs/runner-operator-guide.md` as the runner operator docs page with pool create, join token, runner start, namespace scope, claims and logs, drain and retire. Check: TestDocsRunnerGuideCommands passes and the page uses only the routes ANY /api/runners/{}/pools, ANY /runner/v1, ANY /api/runners/{}/runners/{}/drain, ANY /api/runners/{}/runners/{}/retire, and ANY /api/runners/{}/pools/{}/join-tokens/{}/revoke.
- [ ] 2.2 Document common errors including expired token and wrong scope in the NEW file `docs/runner-operator-guide.md`. Check: TestDocsRunnerGuideCommands passes and each error section names one code from runner.token-used, runner.not-found, runner.retired, runner.invalid, or runner.limit and one API call in backticks.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.2 Wire the page into docs navigation with no route or schema change. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
