## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Edit internal/expr/timeout_internal_test.go:52 to replace the fixed under-1s assertion with started plus timeout error plus elapsed at least 900 ms and less than 30 s. Check: TestWorkLayer with count 1 passes after the edit
- [ ] 1.2 In a scratch worktree edit internal/expr/timeout_internal_test.go:52 to disable the timeout limit so the work returns success and prove the new assertion catches a broken limit. Check: TestWorkLayer fails in the scratch worktree with the limit disabled

## 2. Definition of done

- [ ] 2.1 Show stability by running internal/expr/timeout_internal_test.go:52 with 50 repeated runs under the race detector. Check: TestWorkLayer with count 50 under the race detector
- [ ] 2.2 Run the full check gate after the edit to internal/expr/timeout_internal_test.go:52. Check: make check with zero findings. Confirmed by: the driver's gates.
