## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing store and service tests for Create with kind values person, service, and agent, omitted kind default, kind freeze, and legacy rows without kind. Check: TestPrincipalKindStoredAndReturned in pkg/principal fails on main, passes after.
- [ ] 1.2 Add failing audit tests for population from auth context, canonical encoding, hash coverage, absent block compatibility, subject and reason limits, delegation grammar, and atomic principal audit write. Check: TestAuditAgentBlockCoveredByHash in pkg/audit fails on main, passes after.
- [ ] 1.3 Add failing job token tests in NEW file pkg/jobtoken/agentkey_test.go for Mint (NEW agent key support) with agent key round trip, existence check, malformed key rejection, and legacy tokens without a key. Check: TestJobTokenAgentKeyRoundTrip in job token suite fails on main, passes after.

## 2. Build the shapes

- [ ] 2.1 Edit the baseline of each dialect in place for the three additive fields with defaults that keep current rows valid. Edit the baseline files under pkg/store (one per dialect). Check: Baseline builds for both dialects and `sqlc diff` is clean after `make generate`. Confirmed by: the driver's gates.
- [ ] 2.2 Wire kind and agent fields through the existing principal, audit, and token paths with allowlist checks in pkg/principal, pkg/audit, and pkg/jobtoken. Change the audit adapter at internal/api/audit/audit.go:173 (func toAuditEntry). Check: Route test TestRouteHookFailsOnAnUnknownShape still passes and changed routes declare authorization actions.
- [ ] 2.3 Update the API description in internal/api/apidesc/openapi.json for the new stored fields with no new routes. Check: API description build passes and lists kind, agent block, and agent key fields.
- [ ] 2.4 Add e2e coverage against the real directory for create, read, and audit read in NEW file internal/servertest/agent_shapes_test.go. Check: e2e step calls ANY /api/server/principals/{} and ANY /api/audit/entries/{} with a session credential, passes the existing read gates, and reads back kind and agent block. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Document the stored shapes and the rule that the agent kind alone grants nothing. Check: Docs page commands run as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/agent_shapes_before_first_tag_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Run a usability check with a site administrator. Check: Persona completes the path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Run the full definition of done including wiring, route actions, e2e step, docs page, and usability check. Check: `make check` with zero findings after wiring through ANY /api/server/principals and ANY /api/audit/entries with a session credential and the existing read gates. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
