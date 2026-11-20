## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main (and in the console repository), and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 Confirm that the enforcement change is on main: `GET /api/dustpan/v1/me/tokens?status=expiring` answers 200. Check: `internal/api/server/caller_routes_test.go:157` expects 200 for `status=active`. If it does not, this change waits.

## 1. Failing tests first

- [ ] 1.1 Add a table test that the own list answers `expiryWarning` and `expiresInDays` for each state: far, 13 days, 2 days, expired, revoked, disabled. Check: NEW TestOwnTokenListWarningFields in internal/api/server passes on main, guards: `tokenOf` at internal/api/server/tokens.go:221 already sets both.
- [ ] 1.2 Add a test that the own list with `status=expiring` returns the soon and imminent tokens only. Check: NEW TestOwnTokenListExpiringStatus in internal/api/server fails on main: internal/api/server/tokens.go:819-826 answers 400 for `status` (the enforcement change has the same case; keep one copy, in the change that lands first).

## 2. Implementation

- [ ] 2.1 Hand off the console work: open one issue in the console repository with the facts of the proposal (the route, the query names, no `total`, the banner text, the tests of the banner and of the runtime check). No route: this task writes the hand-off note in the pull request body only. Check: the pull request body links the console issue. The console change merges after the server change that adds the `status` filter, and before the next server tag.

## 3. Side effects (computed from main)

- [ ] 3.1 Confirm that the server needs no regeneration: no route, field or action changes in this repository. Check: `make clients-check` and `make pin-frozen` pass with no diff.

## 4. Definition of done

- [ ] 4.1 Write the docs in the same pull request as the server tests: docs/guides/access-tokens.md (:233) says that the console shows a banner for the tokens that expire within 14 days, and names `status=expiring`. Check: `make docs-lint` passes. No route: the test runs each new command of the page as written only.
- [ ] 4.2 Add an e2e step in e2e/tokenexpiry_test.go: the route is `GET /api/dustpan/v1/me/tokens?status=expiring` and the credential is a personal access token; create a token with a 10 day expiry and a token with a 60 day expiry, list, and check that only the first is in the list with `expiryWarning` soon. Check: the e2e step runs on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the usability check. Check: a Developer finds the banner and the token that ends from the page alone. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings in this repository, and `npm run check` in the console. Confirmed by: CI (the agent runs the fast checks only).
