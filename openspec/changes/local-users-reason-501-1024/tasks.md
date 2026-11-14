## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server tests in pkg/dustpan/localusers_test.go:203 for create reasons of 500, 501, 1024, and 1025 runes that call route `POST /api/dustpan/v1/local-users` with credential `access token` through the authorization gate Check: POST /api/dustpan/v1/local-users with 501 runes fails on main with 500 in pkg/dustpan/localusers_test.go:203.
- [ ] 1.2 Add failing server tests in pkg/dustpan/localusers_test.go:203 that call route `POST /api/dustpan/v1/local-users/unlock` with credential `access token` through the authorization gate and show an overlong reason changes no state Check: Route `POST /api/dustpan/v1/local-users/unlock` with 501 and 1025 runes fails on main with credential `access token` through the authorization gate.
- [ ] 1.3 Add failing multibyte boundary test in pkg/dustpan/localusers_test.go:203 with 500 multibyte Unicode code points Check: Multibyte reason of 500 runes passes the length check.

## 2. Fix

- [ ] 2.1 Use one shared 500-rune limit in the create and unlock paths in pkg/dustpan/localusers.go:206 after auth and before any store write Check: POST /api/dustpan/v1/local-users with 501-rune reason answers 400 local-user.reason-too-long.
- [ ] 2.2 Add the NEW error code entry, its spec, and its catalog test in internal/errcode/catalog_localuser.go:33 with text that names the rune limit and the rerun command Check: Catalog test passes for `local-user.reason-too-long`.
- [ ] 2.3 Update the local-user guide page in docs/guides/local-users.md (NEW if absent) with the 500 Unicode code point limit and the error code Check: make docs-lint passes and the page states the 500-rune limit.
- [ ] 2.4 Search the other local-user reason paths in pkg/dustpan/localusers.go:206 for the same length bug and file a follow-up when it shows; BLOCKED by unlisted disable, delete, and reset reason routes on main Check: `rg -n reason pkg/dustpan/localusers.go` reviewed and follow-up filed when the same bug shows.

## 3. Definition of done

- [ ] 3.1 Add e2e step in e2e/coverage_signin.go:138 against route `POST /api/dustpan/v1/local-users` and route `POST /api/dustpan/v1/local-users/unlock` with credential `access token` through the authorization gate. Confirmed by: CI (make e2e) Check: e2e step covers create and unlock overlong reasons. Confirmed by: CI (make e2e)
- [ ] 3.2 Run usability check of the create and unlock error path. A person's step. Check: Site administrator completes the error path from the guide alone. A person's step.
- [ ] 3.3 Run the full definition of done for route `POST /api/dustpan/v1/local-users` with credential `access token` through the authorization gate: wiring, route with authorization actions, e2e, docs, and checks. Confirmed by: CI (make check) Check: Route `POST /api/dustpan/v1/local-users` wiring plus docs verified. Confirmed by: CI (make check)
