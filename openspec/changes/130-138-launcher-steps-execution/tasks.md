## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing hostile step tests for bounds and bad program form. Check: TestLauncherRefusesShellArgs in pkg/launcher/run/run_test.go fails on main.
- [ ] 1.2 Add failing tests for ReportExit (NEW), duplicate report, wrong claim, signal kill, and stream resume in pkg/launcher/run/run_test.go. Check: TestLauncherReportExitAfterRestart fails on main.

- [ ] 1.3 SECURITY. Add a failing hostile test TestLauncherDeniesTokenFromOtherClaim in the existing file pkg/launcher/run/run_test.go. It sends a handoff token from another claim straight to open, stream and exit. Check: the test fails on main and expects denial with code runner.retired.
- [ ] 1.4 SECURITY. Add a failing hostile test TestLauncherFailsClosedOnBadProgram in the existing file pkg/launcher/run/run_test.go. It sends a program that is neither a bare name nor a clean absolute path straight to the check. Check: the test fails on main and expects denial plus an audit entry with claim and step.

## 2. Build

- [ ] 2.1 Build the NEW command `opendustpan job steps validate` for local file checks with machine-readable errors in the NEW file cmd/opendustpan/job_steps_validate.go. Check: `opendustpan job steps validate steps.json` exits 0 on a good file and 2 with code launcher.step-invalid on a bad file, with no server call.
- [ ] 2.2 Wire checked StartStep (NEW), streaming, and ReportExit (NEW) through ANY /runner/v1 with handoff token at open, stream, and exit. Write the launcher side in the existing file pkg/launcher/run/step.go: extend it in place with the NEW behavior. Keep the route declaration and its adapter in the files that main has for ANY /runner/v1. Check: Assignment from route ANY /runner/v1 with credential kind handoff token starts a step with StartStep and no shell under a claim, streams ordered chunks, and ReportExit stores one code.
- [ ] 2.3 SECURITY. Build the authorize layer in the existing file pkg/launcher/run/step.go. It checks that the handoff token owns the claim at open, stream and exit. Check: TestLauncherDeniesTokenFromOtherClaim in pkg/launcher/run/run_test.go passes and a token from another claim gets runner.retired.
- [ ] 2.4 SECURITY. Build the fail-closed layer in the existing file pkg/launcher/run/step.go. It denies on an unknown program form or a check error and writes an audit entry with claim and step. Check: TestLauncherFailsClosedOnBadProgram in pkg/launcher/run/run_test.go passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Write the docs page for step rules and the validate command in the NEW file docs/guides/launcher-steps.md. Check: Docs page for step rules and the validate command runs each command as written. `make docs-lint` passes on each edited guide.
- [ ] 4.2 Run e2e step with real launcher, usability check, and full checks. Check: E2E job on route ANY /runner/v1 with credential kind handoff token runs a real launcher step with no shell, streams logs, and reports one exit; persona completes path from docs page; `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
