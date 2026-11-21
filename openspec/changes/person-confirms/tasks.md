## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal and by the dependency to be written (the change order package). Check: each answer is a repository record, and the dependency is on main.

## 1. Failing tests first

- [ ] 1.1 In the NEW file `pkg/access/change_order_confirm_test.go`, add `TestAgentConfirmRefused`: an agent confirms with a delegation and with its own token. Check: it fails on main. BLOCKED by open questions 1 and 4.
- [ ] 1.2 In the same file, add `TestDelegatorIsAuthorUnderTwoPersonRule`: the delegator confirms and then approves under `authorMayApprove` false and true. Check: it fails on main. BLOCKED by open question 2.
- [ ] 1.3 In the same file, add `TestConfirmNeedsFreshStepWhenEnabled`: with `freshAuthEnabled` on, a confirm with an access token is refused with `mfa.fresh-step-required`, and with the setting off it passes. Check: it fails on main.
- [ ] 1.4 In the same file, add `TestRevokedDelegationKeepsAuthor`: end the delegation after the draft and check the author. Check: it fails on main.

## 2. Build

- [ ] 2.1 Build the confirm and its audit entry in one transaction. BLOCKED by open questions 1 and 3. Check: the tests of 1.1 to 1.4 pass.
- [ ] 2.2 SECURITY. Add the hostile tests of the layers table of `design.md`, and for each guard a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.
- [ ] 2.3 Extend `docs/guides/approval-rules.md` with a confirm section. Check: `make docs-lint` passes, and the e2e step runs each command as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients when a route or an answer changes. Never edit a generated file by hand. Check: the generators leave no diff.
- [ ] 3.2 Add the line of each NEW error code in its place in the name order of the catalog file. BLOCKED by open question 4. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 4. Definition of done

- [ ] 4.1 In the NEW file `e2e/change_order_confirm_test.go`, confirm a draft with a console session, then read the audit log through the route of open question 3 and check that the confirm entry comes before any apply entry. Check: the step passes in CI. BLOCKED by the change order package.
- [ ] 4.2 In the existing file `e2e/approval_rules_test.go`, add the step that sets `authorMayApprove` false and checks that the delegator is refused as second approver. Check: the step passes in CI.
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
