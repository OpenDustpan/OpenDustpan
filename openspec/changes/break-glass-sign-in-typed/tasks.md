## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the reason limits, hostile characters, alert, audit failure, expiry, setting bound and per-request audit. Check: TestBreakGlassReasonBounds, TestBreakGlassReasonControlChars, TestBreakGlassAuditFailureNoSession, TestBreakGlassLimitFromSetting and TestBreakGlassRequestAuditFailure fail on main
- [ ] 1.2 Write failing tests for a failed assertion with a valid reason, the exempt sign-out, and a tampered cookie. Check: TestBreakGlassBadAssertionNoAlert, TestBreakGlassSignOutExempt and TestBreakGlassTamperedCookie fail on main
- [ ] 1.3 Write failing tests for the stale flag at 89 and 91 days, a refused sign-in, an account never signed in and an identity provider start. Check: TestBreakGlassStaleFlag89And91Days, TestBreakGlassRefusedNotATest, TestBreakGlassNeverSignedIn and TestBreakGlassIdPStartedRefused fail on main
- [ ] 1.4 Write a conformance test for the store method that records the last test. Check: `go test -timeout 60m ./pkg/store/...` fails on main
- [ ] 1.5 Write a failing test for the early end by session generation. Check: TestBreakGlassEarlyEnd fails on main

## 2. Implementation

- [ ] 2.1 Add the claims constructor, alert, notification, WARN line, generation read, setting read and expiry check in pkg/session. Check: `go test ./pkg/session/...`
- [ ] 2.2 Edit the baseline of each dialect for the last-test column, add the store method and update the table list. Check: `sqlc diff` is clean and the conformance test passes on both adapters
- [ ] 2.3 Add the last test time and the stale flag to the principal response. Check: `go test ./internal/api/server/...`
- [ ] 2.4 Add the console banner with the docs link. Check: Browser test of the console repository shows the banner

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the route with its authorization actions. Check: The route test lists the sign-in route with its actions
- [ ] 4.2 Add an e2e step. BLOCKED by passkey sign-in for local users (issue 607). Then it drives `POST /api/dustpan/v1/sign-in/passkey/finish` with a passkey credential and a typed reason, and reads the flag from the principals route. Check: `make e2e` drives the sign-in with the real binary over TLS and reads the flag
- [ ] 4.3 Add the docs page in the same pull request. Check: `make docs-lint` and a test that runs each command
- [ ] 4.4 Run the usability check. Check: The persona completes the path from the docs page and each error names one command
- [ ] 4.5 Run the full check. Check: `make check` reports zero findings
