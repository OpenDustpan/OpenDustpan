## 1. failing tests

- [ ] 1.1 Add failing tests for permit-body refusal, collection-scope refusal, and expired-grant denial. Check: TestAccessLimitPermitRefused fails on main, passes after the fix.
- [ ] 1.2 Add failing tests for access-review expired marking and decision-record refusal. Check: TestGrantExpiryReviewMarksExpired fails on main, passes after the fix.

## 2. implementation

- [ ] 2.1 Add expiresAt to grant store, check and sweep; enforce forbid-only save checks. Check: `go test ./pkg/access/... ./pkg/gate/...` passes.
- [ ] 2.2 Add limit objects, routes, Cedar evaluation after grants, audit entries and docs page. Check: `go test ./internal/...accesslimit...` passes; routes declare scope actions and fresh step.

## 3. definition of done

- [ ] 3.1 Wire routes, run e2e access-limit and expiry steps, add docs page and usability check. Check: Run page and review show limits next to grants; e2e step drives real API over TLS.
- [ ] 3.2 Run full check with wiring, route actions, e2e, docs and usability. Check: `make check` reports zero findings.
