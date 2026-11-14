## 1. Failing e2e tests first

- [ ] 1.1 Add failing e2e tests for pool, join-token, runner list, drain and retire routes. Check: `TestRunnerE2EPoolJoinTokenList` fails on main because no e2e step drives the pool routes.
- [ ] 1.2 Add failing e2e tests for real runner plan, apply, cancel, drain, retire, refusals, claim expiry and Idempotency-Key replay. Check: `TestRunnerE2EPlanApplyCancelDrainRetire` fails on main because no real runner claims the jobs.

## 2. Wiring and routes

- [ ] 2.1 Check authorization actions and Idempotency-Key handling on each driven route. Check: Route test passes and each runner route declares its read or manage action with its object.
- [ ] 2.2 Confirm this part changes no path and follows the dependency for prefix lists and maintenance gate. Check: `rg -n poolsPath internal/api/runnerapi` shows this part changes no path and follows the dependency for prefix lists.

## 3. Definition of done

- [ ] 3.1 Run the full real-runner flow with plan, apply, cancel, drain, retire and no retry mask. Check: `make e2e` passes on SQLite and PostgreSQL over TLS against the real binary.
- [ ] 3.2 Add the operator docs page for pool setup, join, drain and retire. Check: Docs page commands run as written in a clean installation.
- [ ] 3.3 Run usability check, route test, e2e step and full check gate. Check: `make check` reports zero findings.
