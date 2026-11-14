## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Test that a window of 0 refuses age 0 and an old step: TestFreshWindowZeroAsksEachTime in the existing file internal/sessionauth/sessionauth_test.go (for Fresh at internal/sessionauth/sessionauth.go:312) and in NEW file internal/api/server/freshness_test.go (package server, edited by this change) for the route. Check: TestFreshWindowZeroAsksEachTime fails on main
- [ ] 1.2 Test the 403 names the sign-in path in NEW file internal/api/server/freshness_test.go (package server, edited by this change). Check: TestStaleErrorNamesSignInPath fails on main
- [ ] 1.3 Test that an age equal to a nonzero window is fresh: TestFreshBoundaryEqualsWindow in the existing file internal/sessionauth/sessionauth_test.go. Check: TestFreshBoundaryEqualsWindow passes on main and keeps passing
- [ ] 1.4 Test that a window outside 0 and 60 to 900 seconds is refused: TestFreshWindowConstructorRefusesOutOfRange in the existing file pkg/session/policy_test.go, for ValidTokenFreshness (pkg/session/policy.go:35), with 1 s, 59 s, 901 s and 7 days refused and 0, 60 s and 900 s accepted. Check: TestFreshWindowConstructorRefusesOutOfRange fails on main

## 2. Implementation

- [ ] 2.0 In internal/sessionauth/sessionauth.go, make Fresh (:312) return false for a window of 0, so that each caller (the TFE, issuer, profile and Pulumi paths) asks for a fresh authentication at each step, as ADR 227 decision 3 says: "A window of 0 asks for a fresh authentication at each step. No value turns the check off." Change its doc comment. In pkg/session/policy.go, make ValidTokenFreshness (:35) accept 0 or 60 to 900 seconds only (today the upper bound is MaxLengthBound). Check: `go test ./internal/sessionauth/... ./pkg/session/...` passes

## 2b. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Comment on fresh() names mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go
- [ ] 3.2 Route keeps its authorization actions Check: Route test passes
- [ ] 3.3 E2E step: POST /api/dustpan/v1/sign-in/providers/{provider}/password with a stale session cookie, then a guarded step, checks the 403 path Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 docs/guides/console-sign-in.md lists the 403 text and says that a window of 0 asks at each step and that the other values are 60 to 900 seconds. Check: make docs-lint
- [ ] 3.5 Usability check from the docs page alone Check: Site administrator completes the path. A person's step.
- [ ] 3.6 Run make check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
