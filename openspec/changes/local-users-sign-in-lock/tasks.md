## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing ceiling test in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes): 30 made-up names in one fixed UTC hour give at most 10 notices and one updated summary with no user name. Check: `pkg/dustpan/localusers_test.go:203` NEW test TestLockNoticeCeilingSendsSummaryAfterTen fails on main
- [ ] 1.2 Write the failing policy test in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes): the policy shows the ceiling and its effective value, and a set to 0 is denied. Check: `pkg/dustpan/localusers_test.go:203` NEW test TestLocalUserPolicyShowsNoticeCeiling fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Build the atomic hourly counter and summary update in one transaction on both dialects and wire the ceiling setting in file pkg/dustpan/localusers.go:206 (func newLocalUserService). Run the e2e flood step against POST /api/dustpan/v1/sign-in/password with no credential. Check: e2e flood step on sqlite and postgres against `POST /api/dustpan/v1/sign-in/password` with no credential, plus hour-boundary, failed-store, and parallel-instance cases pass
- [ ] 3.2 Show the ceiling in GET /api/dustpan/v1/local-users/policy and change it through the installation settings resource with installation:manage-sign-in and an audit entry. Check: Policy and settings tests with an access token that holds installation:manage-sign-in pass
- [ ] 3.3 Add the NEW file `docs/reference/local-users` for the local users docs page with the ceiling numbers and check the path from the page alone. Check: Docs page lists 10 notices, the summary contents, and the 1 to 100 range
- [ ] 3.4 Run the full check in file pkg/dustpan/localusers_test.go:203 (func TestLocalPasswordRoutes). Check: `make check` with zero findings, Confirmed by: CI (make check)
