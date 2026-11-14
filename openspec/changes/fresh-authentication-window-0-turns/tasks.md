## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Test window 0 refuses age 0 and an old step in NEW file internal/api/server/freshness_test.go (package server, edited by this change). Check: TestFreshWindowZeroAsksEachTime fails on main
- [ ] 1.2 Test the 403 names the sign-in path in NEW file internal/api/server/freshness_test.go (package server, edited by this change). Check: TestStaleErrorNamesSignInPath fails on main
- [ ] 1.3 Test age equal to a nonzero window is fresh in NEW file internal/api/server/freshness_test.go (package server, edited by this change). Check: TestFreshBoundaryEqualsWindow fails on main
- [ ] 1.4 Test an out-of-range window value is refused in NEW file internal/api/server/freshness_test.go (package server, edited by this change). Check: TestFreshWindowConstructorRefusesOutOfRange fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Comment on fresh() names mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go
- [ ] 3.2 Route keeps its authorization actions Check: Route test passes
- [ ] 3.3 E2E step: POST /api/dustpan/v1/sign-in/providers/{provider}/password with a stale session cookie, then a guarded step, checks the 403 path Check: make e2e
- [ ] 3.4 Docs page lists the 403 text Check: make docs-lint
- [ ] 3.5 Usability check from the docs page alone Check: Site administrator completes the path
- [ ] 3.6 Run make check Check: `make check` with zero findings
