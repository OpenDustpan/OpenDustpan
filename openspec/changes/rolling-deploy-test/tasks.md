## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Confirm on main that the rolling deploy step exists in `e2e/ha_rolling_test.go` (`haRollingDeploy` at :195, `haRoll` at :148, `haCheckOldInstances` at :344) and runs as the step "5 rolling deploy" (`e2e/ha_test.go:118`). Evidence only: no new test. No route: the test drives the real CLIs through the proxy only. Check: `go test -tags e2e ./e2e -run TestHA -v` lists the step "5 rolling deploy". Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add the failing check that an old binary write gets `schema.binary-below-minimum` during the fake migration, in the existing file `e2e/ha_rolling_test.go` (extend `haCheckOldInstances` at :344). No route: the test sends a write to an old instance and reads the error code only. Check: `make e2e-ha` fails on main with the missing check. Confirmed by: CI (the agent writes the e2e test).

## 2. Test step

- [ ] 2.1 Evidence only: three serve binaries run behind a round-robin TLS proxy with the loops of `tofu apply` and `pulumi up` and no failed command (`haRollingDeploy` at `e2e/ha_rolling_test.go:195`). No route: the test drives the real CLIs through the proxy only. Check: `make e2e-ha` passes the step "5 rolling deploy". Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Evidence only: each binary is replaced one at a time with a test-only minimum-version row, and the loops stay green (`haRoll` at `e2e/ha_rolling_test.go:148`, `haMixedVersionMatrix` at :417). No route: the test drives the real CLIs through the proxy only. Check: `make mixed-version-test` passes. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no server logic and no request flow, so no round-trip budget changes.

## 4. Definition of done

- [ ] 4.1 Evidence only: the step is wired into the high availability suite (`e2e/ha_test.go:118`). No route: the test drives the real CLIs through the proxy only. Check: `make e2e-ha` passes the step "5 rolling deploy" against real CLI binaries. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Add a read of `GET /api/server/instances` with an access token to `e2e/ha_rolling_test.go` that lists three instances during the step. Check: `make e2e-ha` passes with the read. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run usability check of rolling replace path. Check: Operator completes replace from existing high availability guide alone with clear version error. A person's step.
- [ ] 4.4 Run full checks. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
