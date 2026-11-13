## 1. Failing tests first

- [ ] 1.1 Add failing service test for valid signed request granting one session with audit entry. Check: `go test ./pkg/breakglass/ -run TestMode2SignedRequestGrantsOneSession -count=1` fails on main.
- [ ] 1.2 Add failing service test for wrong signature and unknown key giving 401 with no session and no review. Check: `go test ./pkg/breakglass/ -run TestMode2WrongSignatureRefused -count=1` fails on main.
- [ ] 1.3 Add failing service test for second use of the same request ID giving 409. Check: `go test ./pkg/breakglass/ -run TestMode2ReplayRefused -count=1` fails on main.
- [ ] 1.4 Add failing service test for recovery on wrong mode giving 403 with no session. Check: `go test ./pkg/breakglass/ -run TestMode2WrongModeRefused -count=1` fails on main.
- [ ] 1.5 Add failing service test for expired, future issued-at, wrong audience or installation, and malformed ID giving 401. Check: `go test ./pkg/breakglass/ -run TestMode2StaleAndMisdirectedRefused -count=1` fails on main.
- [ ] 1.6 Add failing service test for two concurrent submits of one ID giving one session and one 409. Check: `go test ./pkg/breakglass/ -run TestMode2ConcurrentConsumeSingleWinner -count=1` fails on main.
- [ ] 1.7 Add failing store adapter test for duplicate request ID refusal, immutable mode after first boot, and restart after commit still refusing replay. Check: `go test ./pkg/breakglass/... -run TestMode2StoreSingleUse -count=1` fails on main.
- [ ] 1.8 Add failing hostile test for a flood of bad signatures staying bounded while a later valid request from the same address still succeeds. Check: `go test ./pkg/breakglass/ -run TestMode2RefusedFloodBounded -count=1` fails on main.
- [ ] 1.9 Add failing service test that success bumps the session generation and ends prior cookies. Check: `go test ./pkg/breakglass/ -run TestMode2RecoveryEndsPriorSessions -count=1` fails on main.

## 2. Implementation

- [ ] 2.1 Implement mode check, signature check, expiry and audience checks, single-use consume, session issue with generation bump, audit entry, and review notification in one transaction. Check: `go test ./pkg/breakglass/... -count=1` passes.
- [ ] 2.2 Wire routes with authorization actions and public-versus-signed request handling. Check: `curl -k https://localhost:8443/api/dustpan/v1/recovery/status` shows signed-request mode with action checks.
- [ ] 2.3 Build and test host recovery-status, recovery-request, and offline signing commands. Check: `go test ./internal/cli/ -run TestHostRecoveryCommands -count=1` passes and `opendustpan host recovery-request --help` prints stable output.

## 3. Definition of done

- [ ] 3.1 Add e2e step for mode 2 install: turn breakGlassReview.enabled on and check its effective value, then valid request gives one session and one review, bad signature and replay give no session. Check: `make e2e E2E_FOCUS=BreakGlassMode2` passes against real binary over TLS with offline-signed request, replay, and bad signature cases.
- [ ] 3.2 Add console step for the issued session from the user path. Check: Browser check signs in to the console with the issued one-time admin session and completes recovery.
- [ ] 3.3 Write docs page for mode 2 setup and recovery in the same pull request. Check: Docs page commands run as written for first-boot mode choice, review opt-in, offline signing, and recovery.
- [ ] 3.4 Run usability check and fix error text. Check: Operator completes recovery from the docs page alone; each error names the one command that helps.
- [ ] 3.5 Run full check with zero findings. Check: `make check` reports zero findings.
