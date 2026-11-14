## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for state.resources and state.removed in pkg/gate/statewrite_test.go. Check: TestStateWriteFactsHaveResourcesAndRemoved fails on main
- [ ] 1.2 Add failing tests for encrypted bodies in pkg/gate/statewrite_test.go. Check: TestStateWriteEncryptedBodyContinues fails on main
- [ ] 1.3 Add failing tests for records and fixed controls in pkg/gate/statewrite_test.go. Check: TestStateWriteRecordInStepTransaction fails on main
- [ ] 1.4 Add failing tests for approval defaults and limits in pkg/gate/service_test.go. Check: TestLimitedAuthorizerEveryRoute fails on main

## 2. Implementation

- [ ] 2.1 Fill facts, write records and list fixed controls in pkg/gate/statewrite.go:284 and pkg/gate/statechange.go:28. Check: `go test ./pkg/gate/...` passes
- [ ] 2.2 Wire LimitedAuthorizer (pkg/gate/service.go:322) for every route. Name the declaration file internal/api/apidesc/openapi.json and the adapter file internal/api/pulumi/ (NEW file internal/api/pulumi/limited_authorizer.go). Check: `go test ./...` passes for touched packages
- [ ] 2.3 E2e step with the real tofu CLI: lock a workspace, run tofu apply with a personal access token against the TFE-compatible route POST /api/v2/workspaces/{id}/state-versions. The step expects the refusal with the record ID. The Pulumi step is BLOCKED by the exact route name of the update start in internal/api/pulumi, which the author checks in the route table. Check: `make e2e` state.write slice passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.4 Document facts, records, defaults and limits in NEW file docs/state-write-gates.md. Check: `make docs-lint` passes

## 3. Definition of done

- [ ] 3.1 Check wiring, route actions and docs page. Run the usability check from the docs page alone with the real tofu CLI and a personal access token. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
