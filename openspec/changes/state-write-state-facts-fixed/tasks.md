## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for state.resources and state.removed. Check: TestStateWriteFactsHaveResourcesAndRemoved fails on main
- [ ] 1.2 Add failing tests for encrypted bodies. Check: TestStateWriteEncryptedBodyContinues fails on main
- [ ] 1.3 Add failing tests for records and fixed controls. Check: TestStateWriteRecordInStepTransaction fails on main
- [ ] 1.4 Add failing tests for approval defaults and limits. Check: TestLimitedAuthorizerEveryRoute fails on main

## 2. Implementation

- [ ] 2.1 Fill facts, write records and list fixed controls. Check: `go test ./pkg/gate/...` passes
- [ ] 2.2 Wire LimitedAuthorizer for every route. Check: `go test ./...` passes for touched packages
- [ ] 2.3 E2e step with the real tofu CLI: lock a workspace, run tofu apply with a personal access token against the TFE-compatible route POST /api/v2/workspaces/{id}/state-versions. The step expects the refusal with the record ID. The Pulumi step is BLOCKED by the exact route name of the update start in internal/api/pulumi, which the author checks in the route table. Check: `make e2e` state.write slice passes
- [ ] 2.4 Document facts, records, defaults and limits. Check: `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Check wiring, route actions and docs page. Run the usability check from the docs page alone with the real tofu CLI and a personal access token. Check: `make check` with zero findings
