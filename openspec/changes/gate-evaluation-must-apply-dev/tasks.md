## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing Evaluate tests for RuleApprovers, Unmeetable and CountsAsSignature with a fake KindReader. Check: TestEvaluateAppliesRuleApprovers fails on main
- [ ] 1.2 Port ADR 253 table tests 4a and 4b to the Evaluate level. Check: TestEvaluateDevTable4a4b fails on main
- [ ] 1.3 Add failing tests for kind error, unknown kind, dev installation and empty approver list. Check: TestEvaluateKindErrorFailsClosed fails on main

## 2. Implementation

- [ ] 2.1 Wire KindReader into Service.Evaluate with one snapshot per call and document the contract in the doc comment. Check: `go test ./pkg/gate/...` passes

## 3. Definition of done

- [ ] 3.1 E2e step: with a site administrator access token, read GET /api/dustpan/v1/namespaces/{namespace}/gates on the real server. BLOCKED by the missing gate evaluation on main: the denial path itself cannot run until Evaluate exists. Check: e2e step reads the gate list with the access token
- [ ] 3.2 Update the gates docs page with NoApprover and dev-signature behavior. Check: docs page lists both rules
- [ ] 3.3 Usability check of the denial and fix flow from the docs page. Check: Administrator completes the flow from the docs page alone
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings
