## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing test first

- [ ] 1.1 Add failing test for demo-admin per-person routes in file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). Check: TestDemoAdminPerPersonPages fails on main, passes after the change.

## 2. Implementation

- [ ] 2.1 Seed demo-admin, its grants, sign-in, and token in file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). A person's step. Check: `make demo` prints the one-time console sign-in for demo-admin.
- [ ] 2.2 Cover session read, refusals, and expiry for the demo identity in file pkg/dustpan/signin.go:224 (func newConsoleSignIn). A person's step. Check: GET /api/dustpan/v1/me/preferences with the demo personal token answers 200, and reuse plus expiry answers token.invalid.

## 3. Definition of done

- [ ] 3.1 Wire the seed into file Makefile:357, document the demo sign-in, run usability check. A person's step. Check: `make demo` then console check of units, preferences, and access tokens pages shows no error banner.
- [ ] 3.2 Run full check for file cmd/opendustpan/devdemo.go:127 (func writeDemoSignIn). Confirmed by: CI (make check). Check: `make check` reports zero findings.
