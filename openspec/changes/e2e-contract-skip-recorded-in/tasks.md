## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Write a failing mixed-directory test in the e2e suite, in the NEW file e2e/contract_skip_scope_test.go, with one binary run and one in-process run in one directory; BLOCKED by no server route because the check reads report files offline and uses no credential. Check: The mixed-directory contract skip test fails on main. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Scope the skip lookup to the recording run in the e2e contract check; the file that holds skippedScenarios in the e2e suite is confirmed first, and no server route or credential is used. Check: The mixed-directory contract skip test passes.

## 2. Definition of done

- [ ] 2.1 Run the e2e contract tests and make check; BLOCKED by no server route because the check is offline. Check: `make e2e` and `make check` pass with zero findings. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
