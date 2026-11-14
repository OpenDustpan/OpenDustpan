## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing adapter test for the cap and the 5 second fallback in pkg/forge/github Check: `go test ./pkg/forge/github/ -run TestWaitTime -count=1` fails on main, passes after fix
- [ ] 1.2 Add failing service test for one coded answer in pkg/forge Check: `go test ./pkg/forge/ -run TestNoRegistration -count=1` fails on main, passes after fix
- [ ] 1.3 Add failing parse tests for public ranges in pkg/egress Check: `go test ./pkg/egress/ -run TestParseAllowedRanges -count=1` fails on main, passes after fix

## 2. Fix

- [ ] 2.1 Cap waitTime in pkg/forge/github/check.go:441 with an unexported constant and a 5 second fallback Check: `rg -n maxWait pkg/forge/github/check.go` shows the 60 second ceiling
- [ ] 2.2 Unify the no-registration answer of check and create in pkg/forge to the existing coded 409 body with no new error code Check: `go test ./pkg/forge/ -run TestNoRegistration -count=1` passes
- [ ] 2.3 Refuse globally routable ranges in pkg/egress/egress.go:124 Check: `go test ./pkg/egress/ -run TestParseAllowedRanges -count=1` passes

## 3. Definition of done

- [ ] 3.1 E2E step calls the existing route `POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check`, passes its authorization gate with an access token credential, and shows the coded 409. Confirmed by: CI (make e2e). Check: e2e step passes in CI
- [ ] 3.2 Write the git host connections docs page with the 60 second cap, the 409 answer and the parse refusal in docs/guides/ Check: `make docs-lint` passes
- [ ] 3.3 Usability check: the administrator completes check and range setup from the docs page alone. A person's step. Check: Each error names one command
- [ ] 3.4 Run full checks. Confirmed by: the driver's gates. Check: `make check` with zero findings
