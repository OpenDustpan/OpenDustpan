## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing conformance tests for RemovePassword and DeleteUser in pkg/store/internal/localuserstore/localuserstore.go:557 (func RemovePassword) Check: go test ./pkg/store/... -run TestLastCredentialLivePasskeys fails on main
- [ ] 1.2 Add a failing test for the reset reach refusal in internal/localusersvc Check: go test ./internal/localusersvc/... -run TestResetNeedsCoveringReach fails on main
- [ ] 1.3 Add a failing test for the allowCredentials shape in internal/mfasvc/service.go:248 (func Service.BeginAssertion) Check: go test ./internal/api/server/... -run TestPasskeyOptionsShape fails on main

## 2. Implementation

- [ ] 2.1 Read the live passkey count in the removal transaction and fix stale comments Check: go test ./pkg/store/... passes
- [ ] 2.2 Add the reach cover check and the NEW error code `local-user.reset-denied` Check: go test ./internal/localusersvc/... passes
- [ ] 2.3 Return allowCredentials in the passkey start and update the passkey guide and reference Check: go test ./internal/api/server/... passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Verify wiring: POST /api/dustpan/v1/local-users/reset with a site-administrator access token, POST /api/dustpan/v1/sign-in/passkey/begin with no credential, and the docs pages Check: make e2e step and make check with zero findings. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
