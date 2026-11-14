## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a failing test: a valid signed request gives one session and an audit entry. Check: `go test ./pkg/breakglass/ -run TestMode2SignedRequestGrantsOneSession -count=1` fails on main (NEW test).
- [ ] 1.2 Write a failing test: a wrong signature gives 401 and no session. Check: `go test ./pkg/breakglass/ -run TestMode2WrongSignatureRefused -count=1` fails on main (NEW test).
- [ ] 1.3 Write a failing test: a second use gives 409. Check: `go test ./pkg/breakglass/ -run TestMode2ReplayRefused -count=1` fails on main (NEW test).
- [ ] 1.4 Write a failing test: the wrong mode gives 403. Check: `go test ./pkg/breakglass/ -run TestMode2WrongModeRefused -count=1` fails on main (NEW test).
- [ ] 1.5 Write a failing test: stale and misdirected requests give 401. Check: `go test ./pkg/breakglass/ -run TestMode2StaleAndMisdirectedRefused -count=1` fails on main (NEW test).
- [ ] 1.6 Write a failing test: two concurrent submits give one session. Check: `go test ./pkg/breakglass/ -run TestMode2ConcurrentConsumeSingleWinner -count=1` fails on main (NEW test).
- [ ] 1.7 Write a failing store test for duplicate ID and immutable mode. Check: `go test ./pkg/breakglass/... -run TestMode2StoreSingleUse -count=1` fails on main (NEW test).
- [ ] 1.8 Write a failing flood test. Check: `go test ./pkg/breakglass/ -run TestMode2RefusedFloodBounded -count=1` fails on main (NEW test).
- [ ] 1.9 Write a failing test that success ends prior sessions. Check: `go test ./pkg/breakglass/ -run TestMode2RecoveryEndsPriorSessions -count=1` fails on main (NEW test).

## 2. Implementation

- [ ] 2.1 Implement the checks, single-use consume, session issue, generation bump and audit entry in one transaction. Check: `go test ./pkg/breakglass/... -count=1` passes.
- [ ] 2.2 Wire the redeem and status routes with their authorization actions. Check: `GET /api/dustpan/v1/recovery/status` shows the mode (NEW route).
- [ ] 2.3 Build host recovery-status and recovery-request helpers. Check: `go test ./cmd/opendustpan/ -run TestHostRecoveryMode2 -count=1` passes (NEW test).
- [ ] 2.4 Purge used IDs at expiry plus 7 days. Check: `go test ./pkg/breakglass/ -run TestMode2PurgeUsedIDs -count=1` passes (NEW test).

## 3. Blocked until the RFC is accepted

- [ ] 3.1 Review open at the use. BLOCKED by the RFC on the use kind for ADR 142. Check: BLOCKED by the RFC; then `go test ./pkg/breakglass/ -run TestMode2OpensReview -count=1` (NEW test).

## 4. Side effects (computed from main)

- [ ] 4.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 5. Definition of done

- [ ] 5.1 Add an e2e step with POST /api/dustpan/v1/recovery/redeem (NEW route) and a signed-request credential (Ed25519 offline key). Check: `make e2e` step BreakGlassMode2 passes against the real binary over TLS.
- [ ] 5.2 Add the console sign-in step. Check: Browser check signs in with the issued session.
- [ ] 5.3 Write the docs page in the same pull request. Check: Docs commands run as written.
- [ ] 5.4 Run the usability check. Check: Operator completes recovery from the docs page; each error names one command.
- [ ] 5.5 Run the full check. Check: `make check` reports zero findings.
