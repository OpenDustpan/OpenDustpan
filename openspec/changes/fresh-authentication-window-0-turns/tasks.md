## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Test that a window of 0 refuses age 0 and an old step: TestFreshWindowZeroAsksEachTime in the existing file internal/sessionauth/sessionauth_test.go (for Fresh at internal/sessionauth/sessionauth.go:312) and in the existing file internal/api/server/freshness_test.go (package server, edited by this change) for the route. Check: TestFreshWindowZeroAsksEachTime fails on main
- [ ] 1.2 Test the 403 names the sign-in path: TestStaleErrorNamesSignInPath in the existing file internal/api/server/freshness_test.go (package server, edited by this change). A valid session with a stale sign-in, and a valid session under window 0, must answer 403 with mfa.fresh-step-required and the `reauthenticate` path. Change the stale-session tests of freshness_test.go and signin_test.go that expect 401 today. This task extends the file with new behavior. Check: TestStaleErrorNamesSignInPath fails on main, because internal/api/server/signin.go:743 answers 401
- [ ] 1.3 Test that an age equal to a nonzero window is fresh: TestFreshBoundaryEqualsWindow in the existing file internal/sessionauth/sessionauth_test.go. Check: TestFreshBoundaryEqualsWindow passes on main and keeps passing
- [ ] 1.4 Test that a window outside 0 and 60 to 900 seconds is refused: TestFreshWindowConstructorRefusesOutOfRange in the existing file pkg/session/policy_test.go, for ValidTokenFreshness (pkg/session/policy.go:35), with 1 s, 59 s, 901 s and 7 days refused and 0, 60 s and 900 s accepted. Check: TestFreshWindowConstructorRefusesOutOfRange fails on main

## 2. Implementation

- [ ] 2.0 In internal/sessionauth/sessionauth.go, make Fresh (:312) return false for a window of 0, so that each caller (the TFE, issuer, profile and Pulumi paths) asks for a fresh authentication at each step, as ADR 227 decision 3 says: "A window of 0 asks for a fresh authentication at each step. No value turns the check off." Change its doc comment. In pkg/session/policy.go, make ValidTokenFreshness (:35) accept 0 or 60 to 900 seconds only (today the upper bound is MaxLengthBound). Check: `go test ./internal/sessionauth/... ./pkg/session/...` passes

- [ ] 2.1 In internal/api/server/freshness.go, make `reauthenticateURL` answer `/api/dustpan/v1/sign-in/password` for the local-user provider instead of an empty string, and make the 403 text of that session name this path (the one command that helps). The path of the other providers stays the start path with `?fresh=1`. Update the stale-password test at internal/api/server/localusers_test.go:443 to expect 403 and the path. Check: `go test ./internal/api/server/ -run 'TestStaleErrorNamesSignInPath|LocalUser'` passes, and with the empty string restored it fails.
- [ ] 2.2 In internal/api/server/signin.go, make the classifier case for errReauthenticate (:743) answer 403 instead of 401, so that problemFor in internal/api/server/server.go (:604-606) writes 403 with mfa.fresh-step-required and the path of a new sign-in, as ADR 227 decision 6 says: "A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing." This change does not touch the 401 for a session that is not valid at all (missing or expired): that answer stays 401. Update the comment of fresh() in internal/api/server/freshness.go. Check: `go test ./internal/api/server/...` passes, with TestStaleErrorNamesSignInPath

## 2b. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change. The files that this change edits are the ones in the Files list of proposal.md.

## 3. Definition of done

- [ ] 3.1 Comment on fresh() names mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go
- [ ] 3.2 Route keeps its authorization actions Check: Route test passes
- [ ] 3.3 E2E step: POST /api/dustpan/v1/sign-in/providers/{provider}/password with a stale session cookie, then a guarded step, checks the 403 path Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 In docs/guides/console-sign-in.md, section "Tokens from a console session": list the 403 text for a session with an old sign-in (today the page says 401), and say that a window of 0 asks at each step and that the other values are 60 to 900 seconds (today the page says "0 turns the check off" and "60 to 604800"). Check: make docs-lint
- [ ] 3.5 Usability check from the docs page alone Check: Site administrator completes the path. A person's step.
- [ ] 3.6 Run make check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
