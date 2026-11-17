## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing Evaluate tests for RuleApprovers, Unmeetable and CountsAsSignature with a fake KindReader in pkg/gate/approvers_test.go. Check: TestEvaluateAppliesRuleApprovers fails on main
- [ ] 1.2 Port ADR 253 table tests 4a and 4b to the Evaluate level in pkg/gate/approvers_test.go. Check: TestEvaluateDevTable4a4b fails on main
- [ ] 1.3 Add failing tests for kind error, unknown kind, dev installation and empty approver list in pkg/gate/approvers_test.go. Check: TestEvaluateKindErrorFailsClosed fails on main

## 2. Implementation

- [ ] 2.1 Wire KindReader into Service.Evaluate at pkg/gate/service.go:226 with one snapshot per call and document the contract in the doc comment. Check: `go test ./pkg/gate/...` passes
- [ ] 2.2 Apply the same approver rules at the state.write gate in pkg/gate/statewrite.go: requireApproval (:647) and requirementOfRule (:687), with the approval outcome of a unit (`approvals` at :146-157 and the refusal at :311-313), normalize approvers with RuleApprovers (pkg/gate/approvers.go:62), and an Unmeetable requirement denies. Check: `go test ./pkg/gate/... -run StateWrite` passes
- [ ] 2.3 Pass the installation kind to the state.write gates in pkg/dustpan/stateguard.go: newStateWriteGates (:94) gives the gate service its KindReader adapter (gate.NewService at :120), so that a dev installation applies the dev approver rules. The internal tests that build the gate service through newStateWriteGates change with it: pkg/dustpan/stateguard_internal_test.go (:529, :593, :637) and pkg/dustpan/stateguard_groups_internal_test.go (:112). Check: `go test ./pkg/dustpan/... -run StateWrite` passes
- [ ] 2.4 BLOCKED by the owner decision on approval defaults and ownership rules at state.write. ADR 225 decisions 4 and 9 (owner) define their `unless` and `when` over the facts of the planned-change decision point, and ADR 224 decision 16 (owner) gives `state.write/1.0` no plan facts. Until the owner decides, the state.write gate in pkg/gate/statewrite.go applies `RuleApprovers` and the `NoApprover` refusal to the approval rules that a gate names only, as task 2.2 says. Check: the owner decision is recorded in an ADR, and this task names its tests.

## 3. Definition of done

- [ ] 3.1 E2e step: with a site administrator access token, read GET /api/dustpan/v1/namespaces/{namespace}/gates on the real server. BLOCKED by the missing gate evaluation on main: the denial path itself cannot run until Evaluate exists. Check: e2e step reads the gate list with the access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Update the existing guide docs/guides/state-write-gates.md with the NoApprover and dev-signature behavior, in the same pull request as the code. Check: docs/guides/state-write-gates.md lists both rules. `make docs-lint` passes on each edited guide.
- [ ] 3.3 Usability check of the denial and fix flow from the docs page. Check: Administrator completes the flow from the docs page alone. A person's step.
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
