---
version: 1
---

Issue: #3117.

Short form: bug (test only).

## Problem

**Persona: maintainer.** `TestReleaseWorkflowChartRerun`
(e2e/release_workflow_branches_test.go:449) gets the exit code 141 (SIGPIPE)
instead of the expected code, at random, so `make e2e` fails on a correct
workflow and blocks the merge queue.

## Evidence

- CI shows it: the e2e job of the merge queue of #3115 and the e2e job
  132643492597 of #3068 fail with "build run refuses to overwrite a released
  chart: exit code = 141, want 1", and the output lacks "refuses to
  overwrite": the script ended at its first line, before the refusal.
- The step "Push the chart as an OCI artifact to the forge registry"
  (.github/workflows/release.yml:212) starts with
  `echo "$GHCR_TOKEN" | helm registry login ghcr.io --username "$GHCR_ACTOR" --password-stdin`
  (:218). The real `helm` reads the token from stdin before it exits.
- The test runs the step with `bash -eo pipefail` (`runWorkflowScript`,
  e2e/release_workflow_branches_test.go:114) and the stub `helm()` of
  `workflowStubs` (:16). The stub never reads stdin: it returns at once. When
  it returns before `echo` writes, the pipe has no reader, `echo` gets
  SIGPIPE and exits 141, `pipefail` makes 141 the status of the pipeline, and
  `-e` ends the script with 141.
- The stub `docker()` has the same fault for `docker login ... --password-stdin`
  (release.yml:92 and :102). There the pipeline is inside `if !`, so a
  SIGPIPE takes the branch of a failed login at random, and a test of that
  step can see "authentication denied" behavior with no `STUB_LOGIN_FAIL`.
- The workflow is correct: each real tool reads its `--password-stdin`. The
  fault is in the stubs, so this is an ordinary change, not a workflow patch.

## Fix boundary

- In `workflowStubs` (e2e/release_workflow_branches_test.go:16), the
  `helm registry login` branch and the `docker login` branch read and
  discard stdin (`cat >/dev/null`) before they return, as the real tools
  read the password. A stub that fails the login reads stdin too.
- No change to `.github/workflows/release.yml`, and no change to an
  expected exit code or output of a test.

## Check

`go test -tags e2e -count=20 -run 'TestReleaseWorkflow' ./e2e/` passes.

## ADR check

No ADR is touched: the change edits a test stub only. `rg -n "release
workflow|release.yml" docs/adr` finds no decision on the test stubs.

## Open questions

none. Considered: drop `pipefail` in the test (refused: the workflow runs
with `pipefail`, so the test must too); change the workflow to pass the token
in another way (refused: the workflow is correct, and a token in an argument
shows in the process list).
