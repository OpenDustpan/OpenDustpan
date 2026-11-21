## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Write failing tests in NEW file pkg/access/access_limits_test.go for forbid-only-save, collection forbid wins, scope-addition, installation-scope, never-allow and both gate guards. Check: TestAccessLimitPermitRefused fails on main, passes after the fix.
- [ ] 1.2 Write failing tests in NEW file pkg/access/grant_expiry_test.go for grant expiry, check clock, sweep, review and history. Check: TestGrantExpiryReviewMarksExpired fails on main, passes after the fix.
- [ ] 1.3 Write failing tests in NEW file pkg/access/limit_routes_test.go for limit list, get, delete, decision records, label limits and break-glass. Check: TestLimitListGetDelete and TestBreakGlassExempt fail on main, pass after the fix.

## 2. implementation

- [ ] 2.1 Add expiresAt to the grant store, check and leased sweep in NEW file pkg/access/grant_expiry.go. Check: `go test ./pkg/access/... ./pkg/gate/...` passes.
- [ ] 2.3 SECURITY. Build the HTTP route guard for set and delete of access limits: each mutating route declares its scope action and needs a fresh step. Declare it in the route table internal/api/server/routes.go and in its adapter file in the NEW file pkg/access/limit_routes.go. Check: a set or delete call without the action or without a fresh step gets 403 and no write.
- [ ] 2.2 Add limit evaluation after grants and audit entries in the existing file pkg/access/limits.go (extend it; it holds LimitKind on main), and add the docs page in NEW file docs/access-limits.md. Check: `go test ./internal/...` passes; routes declare scope actions and fresh step.

## 2a. hostile tests

- [ ] 2.4 SECURITY. Add hostile test TestLimitRoutesRefuseWithoutActionOrFreshStep in the existing file pkg/access/limit_routes_test.go: send set and delete straight to the HTTP route without the scope action, and again without a fresh step. Check: each case gets 403 and no write; the test fails on main and passes after the fix.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. definition of done

- [ ] 4.2 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
