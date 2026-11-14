## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing test TestRestoredHandoffTokenWithMissingRunnerIsRefused in pkg/runner/runnertest/cases.go that restores a backup that predates the runner and presents its handoff token. Check: NEW test TestRestoredHandoffTokenWithMissingRunnerIsRefused fails on main
- [ ] 1.2 Add failing test TestRestoredSessionWithMissingRunnerIsRefused in pkg/runner/runnertest/cases.go that presents a restored session. Check: NEW test TestRestoredSessionWithMissingRunnerIsRefused fails on main

## 2. Fix

- [ ] 2.1 Apply the stable-ID runner-existence check before consumption at handoff-token and session use, shared by both adapters and verified in pkg/store/storetest/backup_runners.go. Check: Both NEW tests pass on sqlite and postgres with runner.not-found

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the refusal through ANY /runner/v1 with a runner key or handoff token credential. Check: ANY /runner/v1 with orphaned credential returns runner.not-found
- [ ] 4.2 Run the usability check for the orphaned-credential refusal. Check: Operator reads runner.not-found and the command that helps. A person's step.
- [ ] 4.3 Run make check with zero findings. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
