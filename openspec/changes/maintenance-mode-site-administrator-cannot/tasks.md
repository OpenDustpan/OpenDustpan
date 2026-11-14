## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing server test for site administrator session start in maintenance mode in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session with credential kind session from password sign-in through the maintenance gate. Check: NEW server test TestStartSessionInMaintenanceModeAsSiteAdministrator fails on main with maintenance.in-maintenance.
- [ ] 1.2 Add failing atomicity test that a failed audit write creates no session in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session with credential kind session. Check: NEW server test TestSessionAuditAtomicityInMaintenanceMode fails on main by creating a session without its audit entry.
- [ ] 1.3 Add failing test that session policy writes, refresh, and sign-out stay refused for the same caller in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session-policy with credential kind session through the maintenance gate. Check: NEW server test TestNonSessionWriteStayRefusedInMaintenanceMode fails on main by allowing the write.

## 2. Fix

- [ ] 2.1 Open the single session write through the maintenance gate in pkg/maintenance with its audit entry and error mapping in internal/api/problem via ANY /api/server/session with credential kind session. Confirmed by: the driver's gates. Check: ANY /api/server/session in maintenance mode returns 200 for a site administrator with session cookies, and session policy writes still return maintenance.in-maintenance.
- [ ] 2.2 Wire gate, route authorization in internal/api/audit/audit.go, and operator guide text for the fresh session approval via ANY /api/server/audit/chain with credential kind session and e2e step via e2e/signin_token.go:224 (func Env.FreshSession). Confirmed by: the driver's gates. Check: ANY /api/server/audit/chain with the fresh session succeeds, interim guide text says sign in before maintenance, and e2e step uses e2e/signin_token.go:224 (func Env.FreshSession).

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm wiring and route authorization for the session-start exception and the audit chain approval. Confirmed by: the driver's gates. Check: ANY /api/server/session declares its authorization action and ANY /api/server/audit/chain accepts the fresh session in maintenance mode.
- [ ] 4.2 Run e2e step against the real server with a site administrator session credential. Confirmed by: CI (e2e maintenance session path). Check: E2e step starts a session in maintenance mode via ANY /api/server/session then approves via ANY /api/server/audit/chain.
- [ ] 4.3 Update the operator docs page in the same pull request. A person's step. Check: Operator guide page shows sign-in path during maintenance and each refusal names the one help command.
- [ ] 4.4 Complete usability check for the site administrator path. A person's step. Check: Usability walkthrough with only the docs page signs in during maintenance and approves the chain change.
- [ ] 4.5 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` passes with zero findings.
