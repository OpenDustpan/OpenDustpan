## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing e2e step for rolling deploy with mixed version pool in NEW file e2e/ha_rolling_deploy_test.go that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `go test -tags e2e ./e2e -run TestHARollingDeploy -v` fails on main with missing step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Add failing check that old binary writes get schema.binary-below-minimum during fake migration in NEW file e2e/ha_rolling_deploy_refusal_test.go that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `go test -tags e2e ./e2e -run TestHARollingDeployMixedVersionRefusal -v` fails on main with missing check. Confirmed by: CI (the agent writes the e2e test).

## 2. Test step

- [ ] 2.1 Run three serve binaries behind round-robin TLS proxy with tofu apply and pulumi up loops and zero failed commands in NEW file e2e/ha_rolling_deploy_test.go that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make e2e-ha ROLLING_DEPLOY=1` shows proxy record with each of three instances serving each tool and zero failed CLI exits. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Replace binaries one at a time with fake contract migration and keep loops green in NEW file e2e/ha_rolling_deploy_test.go that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make mixed-version-test` passes with test-only minimum bump, and passes with last 2 tags when a v tag exists. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire step into high availability suite with proxy record and operation log that calls ANY /api/server/instances with access token and passes the authorization gate. Check: `make e2e-ha` passes rolling deploy step against real CLI binaries. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Use existing instances route for diagnosis. Check: `GET /api/server/instances` lists three instances during step with access token
- [ ] 4.3 Run usability check of rolling replace path. Check: Operator completes replace from existing high availability guide alone with clear version error. A person's step.
- [ ] 4.4 Run full checks. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
