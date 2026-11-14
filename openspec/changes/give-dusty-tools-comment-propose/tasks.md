## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing catalog test: three tools, no approve name. Check: TestToolCatalogHasNoApprove fails on main.
- [ ] 1.2 Write the failing comment tests for terminal jobs, change-order jobs and overlong text. Check: TestCommentJobRefusesChangeOrderJob fails on main.
- [ ] 1.3 Write the failing draft tests for confirm, agent confirm refusal and replay. Check: TestProposeStaysDraft fails on main.
- [ ] 1.4 Write the failing verdict and audit tests. Check: TestAgentVerdictRefused fails on main.

## 2. Build

- [ ] 2.1 Extend the Toolset in internal/agent with the three tools. Check: The tests above pass.
- [ ] 2.2 Add the user docs page. Check: make docs-lint passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e step against the Dusty loop and the MCP tools/list route with an access token. Check: Toolset call through the Dusty loop with an access token passes the agent:use gate; BLOCKED by dusty-proposes-change-order for the draft route.
- [ ] 4.2 Usability check from the docs page alone. Check: Each error names its one command.
- [ ] 4.3 Run make check. Check: make check reports zero findings.
