## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Test window 0 refuses age 0 and an old step Check: TestFreshWindowZeroAsksEachTime fails on main
- [x] 1.2 Test the 403 names the sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- [x] 1.3 Test age equal to a nonzero window is fresh Check: TestFreshBoundaryEqualsWindow fails on main
- [x] 1.4 Test an out-of-range window value is refused Check: TestFreshWindowConstructorRefusesOutOfRange fails on main

## 2. Side effects (computed from main)

- [x] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Comment on fresh() names mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go
- [x] 3.2 Route keeps its authorization actions Check: Route test passes
- [ ] 3.3 E2E step: POST /api/dustpan/v1/sign-in/providers/{provider}/password with a stale session cookie, then a guarded step, checks the 403 path Check: make e2e
- [x] 3.4 Docs page lists the 403 text Check: make docs-lint
- [ ] 3.5 Usability check from the docs page alone Check: Site administrator completes the path
- [ ] 3.6 Run make check Check: `make check` with zero findings
