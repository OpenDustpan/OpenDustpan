---
version: 1
---

Issue: #2534.

## Problem

TestWorkLayer asserts run() under 1 s and fails with 1.03 s on loaded CI runners, which breaks unrelated pull requests with a flaky failure.

## Evidence

- TestWorkLayer exists at internal/expr/timeout_internal_test.go:52. Proof: internal/expr/timeout_internal_test.go:52 (TestWorkLayer)
- Issue #2534 reports a CI failure with run() at 1.03 s against a want of less than 1 s on a PR that does not touch internal/expr. Proof: Issue #2534 body: internal/expr timeout_internal_test.go:77 failed on a CI runner with "run() took 1.033836807s, want less than 1s" on a PR that does not touch internal/expr

## Fix boundary

- Change internal/expr/timeout_internal_test.go so TestWorkLayer asserts the work started, the result is the timeout error, and elapsed is at least 900 ms and less than 30 s instead of run() under a fixed 1 s bound.

Out of scope:

- Production timeout values
- Other flaky tests

## Check

- Edit internal/expr/timeout_internal_test.go:52 to replace the fixed under-1s assertion with started plus timeout error plus elapsed at least 900 ms and less than 30 s. Check: TestWorkLayer with count 1 passes after the edit
- In a scratch worktree edit internal/expr/timeout_internal_test.go:52 to disable the timeout limit so the work returns success and prove the new assertion catches a broken limit. Check: TestWorkLayer fails in the scratch worktree with the limit disabled
- Show stability by running internal/expr/timeout_internal_test.go:52 with 50 repeated runs under the race detector. Check: TestWorkLayer with count 50 under the race detector
- Run the full check gate after the edit to internal/expr/timeout_internal_test.go:52. Check: make check with zero findings. Confirmed by: the driver's gates.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestWorkLayer exists in internal/expr. (`internal/expr/timeout_internal_test.go:52 (TestWorkLayer)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the fix need a change to production code? No. The fix stays inside the test file named in what_changes with no production change.
- Margin or fake clock, and what margin value? Margin only. The production limit stays at 1 s. The test asserts elapsed at least 900 ms and less than 30 s with the timeout error and the started assertion. No fake clock exists on main for this file, so no clock port is added.
- How does the new test prove it catches a broken limit? The mutation task disables the limit in a scratch worktree and requires the new assertion to fail with success instead of the timeout error.
- Does the test check that work does not stop too early? Yes. The 900 ms lower bound plus the timeout-error assertion fails on an early stop.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
