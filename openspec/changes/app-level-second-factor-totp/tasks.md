## 1. Failing tests first

- [ ] 1.1 Add failing tests for first enrollment with code, step-up restart and refusal shape Check: `go test ./internal/mfasvc -run TestTotpEnrollWithCode -count=1` fails on main with no factor routes
- [ ] 1.2 Add failing test for single-use TOTP and WebAuthn challenges across restart Check: `go test ./internal/mfasvc -run TestReplayedCodeFails -count=1` fails on main
- [ ] 1.3 Add failing test for the SSH-signature fresh-step factor path Check: `go test ./internal/mfasvc -run TestSshSignatureFreshStep -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Add factor enrollment, removal, step-up and enrollment-code routes with actions and policy checks in internal/mfasvc Check: `GET /api/dustpan/v1/me/factors` lists enrolled factors with declared actions and passes the route test
- [ ] 2.2 Reuse the local-user MFA factor code for check and audit entries; call the mfa factor store and reset Check: `go test ./internal/mfasvc -count=1` passes with TOTP, passkey and SSH paths sharing one code path
- [ ] 2.3 Cover restore into a new installation with an undecryptable factor seal and re-enrollment Check: `go test ./internal/mfasvc -run TestRestoreLeavesFactorsUnreadable -count=1` passes on SQLite and PostgreSQL
- [ ] 2.4 Add e2e step against the real server with an OAuth test provider session Check: E2e step enrolls a TOTP factor, passes a passkey check and passes an SSH-signature check from an OAuth test-provider session through the real server on SQLite and on PostgreSQL over TLS

## 3. Definition of done

- [ ] 3.1 Wiring, route with authorization actions Check: Route table lists each new route with its action; public list unchanged except named factor paths
- [ ] 3.2 E2e step against the real server on both dialects Check: `make e2e` OAuth factor step passes on SQLite and on PostgreSQL over TLS through the real binary
- [ ] 3.3 Docs page and usability check with the persona path alone Check: Docs page commands run as written and each error names the one command that helps
- [ ] 3.4 Full check with zero findings Check: `make check` reports zero findings
