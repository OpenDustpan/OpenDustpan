## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing e2e tests first

- [ ] 1.1 Add failing e2e tests that drive ANY /api/runners/{}/pools with an access token and ANY /runner/v1 join with a join token, and list each route in e2e ServedRoutes (e2e/coverage.go:42). Write the tests in NEW file e2e/runner_real_test.go. Check: `TestRunnerE2EPoolJoinTokenList` fails on main because no e2e step drives the pool routes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add failing e2e tests that drive ANY /runner/v1 poll and claim with a runner key and ANY /api/runners/{}/runners/{}/drain and ANY /api/runners/{}/runners/{}/retire with an access token. Write the tests in NEW file e2e/runner_real_flow_test.go. Check: `TestRunnerE2EPlanApplyCancelDrainRetire` fails on main because no real runner claims the jobs. Confirmed by: CI (the agent writes the e2e test).

## 2. Wiring and routes

- [ ] 2.1 Check authorization actions and Idempotency-Key handling on each driven route with an access token on ANY /api/runners/{}/pools and ANY /api/runners/{}/runners/{}/retire. Read the declarations in internal/api/runnerapi/routes.go:76 and add the route test to internal/api/runnerapi/flow_test.go. Check: Route test passes and each runner route declares its read or manage action with its object.
- [ ] 2.2 Confirm this part changes no path and follows the pool-route-move dependency for prefix lists and maintenance gate. Check: `rg -n poolsPath internal/api/runnerapi` shows this part changes no path and follows the dependency for prefix lists.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the full real-runner flow with plan, apply, cancel, drain, retire and no retry mask through ANY /runner/v1 with a runner key and drain and retire with an access token. Check: `make e2e` passes on SQLite and PostgreSQL over TLS against the real binary through ANY /runner/v1 with a runner key and ANY /api/runners/{}/pools with an access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Add the operator docs page for pool setup, join, drain and retire. Check: Docs page commands run as written in a clean installation. Confirmed by: CI (an e2e step in the NEW file e2e/add_e2e_step_runs_real_runner_docs_test.go runs each command of the docs page against the e2e test server). A person's step.
- [ ] 4.3 Run usability check, route test, e2e step against ANY /runner/v1 with a runner key and full check gate. Check: `make check` reports zero findings after the e2e step against ANY /runner/v1 with a runner key and ANY /api/runners/{}/pools with an access token. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
