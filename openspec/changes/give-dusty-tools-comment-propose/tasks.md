## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing catalog test in internal/agent/tools_test.go: three tools, no approve name. Move the maximum of TestCatalogSize from 12 to 15 (ADR 367 decision 2). Check: TestToolCatalogHasNoApprove fails on main.
- [ ] 1.2 Write the failing comment tests in internal/agent/changes_test.go for terminal jobs, change-order jobs and overlong text. Check: TestCommentJobRefusesChangeOrderJob fails on main.
- [ ] 1.3 Write the failing draft tests in internal/agent/gate_test.go for confirm, agent confirm refusal and replay. Check: TestProposeStaysDraft fails on main.
- [ ] 1.4 Write the failing verdict and audit tests in pkg/audit/changes_test.go. Check: TestAgentVerdictRefused fails on main.

## 2. Build

- [ ] 2.1 Extend the Toolset in internal/agent with the three tools. Check: The tests above pass.
- [ ] 2.2 Add the user docs page at docs/user/dusty-comment-propose.md (NEW). Check: make docs-lint passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the e2e step against the Dusty loop and the MCP tools/list route with an access token. Check: Toolset call through the Dusty loop with an access token passes the agent:use gate; BLOCKED by dusty-proposes-change-order for the draft route. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Usability check from the docs page alone. Check: Each error names its one command. A person's step.
- [ ] 4.3 Run make check. Check: make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
