## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the reason limits, hostile characters, alert, audit failure, expiry, setting bound and per-request audit in pkg/session/breakglass_test.go. Check: TestBreakGlassReasonBounds, TestBreakGlassReasonControlChars, TestBreakGlassAuditFailureNoSession, TestBreakGlassLimitFromSetting and TestBreakGlassRequestAuditFailure fail on main
- [ ] 1.2 Write failing tests for a failed assertion with a valid reason, the exempt sign-out, and a tampered cookie in pkg/session/breakglass_test.go. Check: TestBreakGlassBadAssertionNoAlert, TestBreakGlassSignOutExempt and TestBreakGlassTamperedCookie fail on main
- [ ] 1.3 Write failing tests for the stale flag at 89 and 91 days, a refused sign-in, an account never signed in and an identity provider start in pkg/session/breakglass_test.go. Check: TestBreakGlassStaleFlag89And91Days, TestBreakGlassRefusedNotATest, TestBreakGlassNeverSignedIn and TestBreakGlassIdPStartedRefused fail on main
- [ ] 1.4 Write a conformance test for the store method that records the last test in the NEW file pkg/store/breakglass_conformance_test.go. Check: `go test -timeout 60m ./pkg/store/...` fails on main
- [ ] 1.5 Write a failing test for the early end by session generation in pkg/session/generation_test.go. Check: TestBreakGlassEarlyEnd fails on main

## 2. Implementation

- [ ] 2.1 Add the claims constructor, alert, notification, WARN line, generation read, setting read and expiry check in pkg/session. Check: `go test ./pkg/session/...`
- [ ] 2.2 Edit the baseline migration of each dialect for the last-test column, add the store method and update the table list. The baseline files are NEW paths for this task: pkg/store/sqlite/migrations/0001_baseline.sql and pkg/store/postgres/migrations/0001_baseline.sql. The store port method is NEW in pkg/store/store.go. Check: `sqlc diff` is clean and the conformance test passes on both adapters
- [ ] 2.3 Add the last test time and the stale flag to the principal response in internal/api/server/principals.go. Check: `go test ./internal/api/server/...`
- [ ] 2.4 Add the console banner with the docs link in the console repository (OpenDustpan/console), in the NEW file src/BreakGlassStaleBanner.tsx. Check: Browser test of the console repository shows the banner

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the route with its authorization actions in the route declaration internal/api/server/passkeysignin.go:122 and its adapter file internal/api/server/passkeysignin.go. Check: The route test lists the sign-in route with its actions
- [ ] 4.2 Add an e2e step. BLOCKED by passkey sign-in for local users (issue 607). Then it drives `POST /api/dustpan/v1/sign-in/passkey/finish` with a passkey credential and a typed reason, and reads the flag from the principals route. Check: `make e2e` drives the sign-in with the real binary over TLS and reads the flag. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Add the docs page in the same pull request. Check: `make docs-lint` and a test that runs each command
- [ ] 4.4 Run the usability check. Check: The persona completes the path from the docs page and each error names one command. A person's step.
- [ ] 4.5 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
