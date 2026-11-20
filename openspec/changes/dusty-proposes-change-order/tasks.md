## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal and by the dependencies to be written (the change order package and the model connect). Check: each answer is a repository record, and each dependency is on main.

## 1. Failing tests

- [ ] 1.1 In the NEW file `internal/agent/propose_test.go`, add failing tests: `TestDustyDraftNamesPersonAgentAndDelegation`, `TestDustyDraftAfterEndIsRefused`, `TestDustyDraftOutsideScopeIsRefused`, `TestDustyDraftOnceForOneKey`. Check: each fails on main. BLOCKED by open questions 1 and 2.
- [ ] 1.2 Add the line of each NEW error code in its place in the name order of the catalog file. BLOCKED by open question 4. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 2. Build

- [ ] 2.1 Write the draft with the delegation resolved in the same transaction. BLOCKED by open questions 1, 2 and 5. Check: the tests of 1.1 pass.
- [ ] 2.2 SECURITY. Add the hostile tests of the layers table of `design.md`, and for each guard a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients when a route or an answer changes. Never edit a generated file by hand. Check: the generators leave no diff.

## 4. Definition of done

- [ ] 4.1 Add an e2e step for the draft in the NEW file `e2e/dusty_draft_test.go`. Check: the step passes in CI. BLOCKED by the Dusty run job.
- [ ] 4.2 Write the guide in the NEW file `docs/guides/dusty-draft.md` and check the user path from the page alone. Check: `make docs-lint` passes. A person's step.
- [ ] 4.3 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
