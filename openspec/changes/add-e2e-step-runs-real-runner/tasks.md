## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing e2e tests first

- [ ] 1.1 Add failing e2e tests that drive ANY /api/runners/{}/pools with an access token and ANY /runner/v1 join with a join token, and list each route in e2e ServedRoutes. Check: `TestRunnerE2EPoolJoinTokenList` fails on main because no e2e step drives the pool routes.
- [ ] 1.2 Add failing e2e tests that drive ANY /runner/v1 poll and claim with a runner key and ANY /api/runners/{}/runners/{}/drain and ANY /api/runners/{}/runners/{}/retire with an access token. Check: `TestRunnerE2EPlanApplyCancelDrainRetire` fails on main because no real runner claims the jobs.

## 2. Wiring and routes

- [ ] 2.1 Check authorization actions and Idempotency-Key handling on each driven route with an access token on ANY /api/runners/{}/pools and ANY /api/runners/{}/runners/{}/retire. Check: Route test passes and each runner route declares its read or manage action with its object.
- [ ] 2.2 Confirm this part changes no path and follows the pool-route-move dependency for prefix lists and maintenance gate. Check: `rg -n poolsPath internal/api/runnerapi` shows this part changes no path and follows the dependency for prefix lists.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the full real-runner flow with plan, apply, cancel, drain, retire and no retry mask through ANY /runner/v1 with a runner key and drain and retire with an access token. Check: `make e2e` passes on SQLite and PostgreSQL over TLS against the real binary through ANY /runner/v1 with a runner key and ANY /api/runners/{}/pools with an access token.
- [ ] 4.2 Add the operator docs page for pool setup, join, drain and retire. Check: Docs page commands run as written in a clean installation.
- [ ] 4.3 Run usability check, route test, e2e step against ANY /runner/v1 with a runner key and full check gate. Check: `make check` reports zero findings after the e2e step against ANY /runner/v1 with a runner key and ANY /api/runners/{}/pools with an access token.
