## 1. Fix the stubs

- [ ] 1.1 No route: the test runs the step scripts of the release workflow with stub tools only. In `workflowStubs` (e2e/release_workflow_branches_test.go:16), make the `helm()` stub read and discard stdin for `helm registry login`, and the `docker()` stub read and discard stdin for `docker login`, before each returns (also on a stubbed failure). Check: `go test -tags e2e -count=20 -run 'TestReleaseWorkflow' ./e2e/` passes.
- [ ] 1.2 No route: the test runs the step scripts of the release workflow with stub tools only. Add a case to the table test of `TestReleaseWorkflowChartRerun` in e2e/release_workflow_branches_test.go that sets a token of 100 KiB in `GHCR_TOKEN` (more than the 64 KiB pipe buffer of Linux, and less than the 128 KiB limit of one environment string), so that `echo` cannot finish before the stub reads; the case expects exit code 0 for a missing chart. Mutation: remove the read of stdin from the `helm()` stub; the case fails with 141. Check: `go test -tags e2e -count=20 -run TestReleaseWorkflowChartRerun ./e2e/` passes.

## 2. Definition of done

- [ ] 2.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
