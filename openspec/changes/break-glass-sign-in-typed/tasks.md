## 1. Failing tests first

- [ ] 1.1 Write failing tests for the reason limits, hostile characters, alert, audit failure, expiry, setting bound and per-request audit. Check: TestBreakGlassReasonBounds, TestBreakGlassReasonControlChars, TestBreakGlassAuditFailureNoSession, TestBreakGlassLimitFromSetting and TestBreakGlassRequestAuditFailure fail on main
- [ ] 1.2 Write failing tests for the stale flag at 89 and 91 days, a refused sign-in, an account never signed in and an identity provider start. Check: TestBreakGlassStaleFlag89And91Days, TestBreakGlassRefusedNotATest, TestBreakGlassNeverSignedIn and TestBreakGlassIdPStartedRefused fail on main
- [ ] 1.3 Write a conformance test for the store method that records the last test. Check: `go test -timeout 60m ./pkg/store/...` fails on main
- [ ] 1.4 Write failing tests for the early end and the unchanged count of enabled accounts. Check: TestBreakGlassEarlyEnd and TestBreakGlassFloorUnchanged fail on main

## 2. Implementation

- [ ] 2.1 Add the claims constructor, alert, WARN line, generation read, setting read and expiry check in pkg/session. Check: `go test ./pkg/session/...`
- [ ] 2.2 Edit the baseline of each dialect for the last-test column, add the store method and update the table list. Check: `sqlc diff` is clean and the conformance test passes on both adapters
- [ ] 2.3 Add the last test time and the stale flag to the principal response. Check: `go test ./internal/api/server/...`
- [ ] 2.4 Add the console banner with the docs link. Check: Browser test of the console repository shows the banner

## 3. Definition of done

- [ ] 3.1 Wire the route with its authorization actions. Check: The route test lists the sign-in route with its actions
- [ ] 3.2 Add an e2e step against the real binary over TLS. Check: `make e2e` drives a break-glass sign-in and reads the flag
- [ ] 3.3 Add the docs page in the same pull request. Check: `make docs-lint` and a test that runs each command
- [ ] 3.4 Run the usability check. Check: The persona completes the path from the docs page and each error names one command
- [ ] 3.5 Run the full check. Check: `make check` reports zero findings
