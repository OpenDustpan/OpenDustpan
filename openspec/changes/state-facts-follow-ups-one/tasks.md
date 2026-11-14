## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add strict-envelope cases to TestClientEncryptionPathsAgree in pkg/workspace/rollback_change_internal_test.go:112 Check: `go test ./pkg/workspace/ -run TestClientEncryptionPathsAgree -count=1` fails on main
- [ ] 1.2 Add a failing over-limit refusal test in NEW internal/api/stategate/limit_test.go Check: `go test ./internal/api/stategate/ -run TestOverLimitStateGivesGateLimit -count=1`

## 2. Fix

- [ ] 2.1 Make IsClientEncrypted strict in pkg/workspace/tofu_state.go:143 and add the loose stored reader there Check: `go test ./pkg/workspace/ -run TestClientEncryptionPathsAgree -count=1` passes
- [ ] 2.2 Delete the ErrLimit branch in internal/api/stategate/stategate.go:377 (func stateUnreadable) Check: `go test ./internal/api/stategate/ -run TestOverLimitStateGivesGateLimit -count=1` passes
- [ ] 2.3 Replace the 30 s assertion in pkg/workspace/rollback_change_internal_test.go:71 with an attempt bound Check: `go test ./pkg/workspace/ -run TestRollbackChangeLimit -count=20` passes under load

## 3. Definition of done

- [ ] 3.1 Run an e2e step with real OpenTofu: upload and restore through ANY /api/v2/state-versions/{}/actions/restore with an access token — Confirmed by: CI (make e2e) Check: `make e2e`
- [ ] 3.2 Update the restore docs page, run the Developer path check and run make check — Confirmed by: CI (make check) Check: `make check` with zero findings
