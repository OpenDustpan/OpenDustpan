## 1. Failing tests first

- [ ] 1.1 Add failing Evaluate tests for RuleApprovers, Unmeetable, and CountsAsSignature with a fake KindReader. Check: TestEvaluate_AppliesRuleApprovers fails on main with errNotImplemented.
- [ ] 1.2 Port ADR 253 table tests 4a and 4b to the Evaluate level with dev and NoApprover subjects. Check: TestEvaluate_DevTable4a4b_AtEvaluateLevel fails on main.
- [ ] 1.3 Add failing tests for KindReader error, unknown kind, mixed approvers, evaluate mode, ownership rules, hostile dev spellings, audit entries, job hold, re-evaluation, and status codes. Check: TestEvaluate_KindErrorFailsClosed fails on main.

## 2. Implementation

- [ ] 2.1 Wire KindReader into Service.Evaluate and call RuleApprovers, Unmeetable, and CountsAsSignature with one kind snapshot. Check: `go test ./pkg/gate/...` passes with the new tests.
- [ ] 2.2 Update the Service.Evaluate doc comment with the approver rule contract. Check: godoc for Service.Evaluate names the approver policy, Production source, and fail-closed rule.
- [ ] 2.3 Open the RFC on whether temp_admins is dropped or re-validated on the KeepAccessTables restore path. Check: RFC draft filed for temp_admins on the KeepAccessTables path.

## 3. Definition of done

- [ ] 3.1 Run the gate and job suites with race. Check: `go test -race ./pkg/gate/... ./pkg/job/...` passes.
- [ ] 3.2 Add an e2e step against the real API for the unmeetable denial path. Check: E2e step drives the real API: set a NoApprover rule, start a job, and read the 409 decision with its audit entry.
- [ ] 3.3 Run a usability check of the denial and fix flow. Check: Namespace administrator completes the user path from the docs page alone with no surprise.
- [ ] 3.4 Run the full check with zero findings. Check: `make check` reports zero findings.
- [ ] 3.5 Update the user docs page for gates in the same pull request. Check: Docs page for gate approver rules lists NoApprover and dev-signature behavior on production.
