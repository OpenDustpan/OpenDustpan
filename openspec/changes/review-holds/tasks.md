## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for refusal of fan-out apply job start before apply request. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyBlockedBeforeApproval fails on main with access token
- [ ] 1.2 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for start of fan-out apply jobs after the apply request for the named revision. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyStartsAfterApproval fails on main with access token
- [ ] 1.3 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for missing grant and self approval refusal with audit entry. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyRefusesSelfApproval fails on main with access token
- [ ] 1.4 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for void on new revision including running-job mark. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyVoidOnNewRevision fails on main with access token
- [ ] 1.5 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for duplicate request, retry after rejection, and runner restart. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyStartsEachUnitOnce fails on main with access token
- [ ] 1.6 Add failing server test in NEW file internal/server/changeorder_apply_gate_test.go for exact 403 code, message text, empty-plan skip, and failed-plan refusal. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyGatedMessage fails on main with access token
- [ ] 1.7 Add failing e2e step in NEW file e2e/changeorder_apply_gate_test.go with recording test provider over 3 units. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: e2e step with real OpenTofu plus Terragrunt over 3 units checks zero provider apply calls before the apply request with access token. Confirmed by: CI (the agent writes the e2e test).

## 2. Build

- [ ] 2.1 Wire the applyGate check (NEW type in NEW file internal/server/changeorder_apply_gate.go) into the apply job start path with approve action on the change order. Declare the NEW route in NEW file internal/server/changeorder_routes.go and add its adapter in NEW file internal/api/changeorder_apply_request.go. Add the NEW error code `change-order.apply-gated` in internal/errcode/catalog_request.go. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request with approve action on the change order using access token: start returns 403 before and starts after
- [ ] 2.2 Add the CLI command for the apply request in NEW file cmd/opendustpan/changeorder_apply_request.go with its own test in NEW file cmd/opendustpan/changeorder_apply_request_test.go. Check: NEW command `opendustpan change-order apply-request --id <id>` sends the apply request and shows the stored revision in a test with access token
- [ ] 2.3 Wire the audit entry for the apply request path in NEW file internal/server/changeorder_apply_gate.go. Check: Denied and stored apply requests each write one audit entry with principal and revision
- [ ] 2.4 Wire void on new plan revision with running-job mark in NEW file internal/server/changeorder_apply_gate.go. Check: New plan revision voids the stored request in the same transaction that publishes the revision
- [ ] 2.5 Write the Terragrunt run-all review gate docs page in NEW file docs/terragrunt-run-all-review-gate.md in the same pull request. Check: Docs page command sequence runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/review_holds_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run e2e step against the real CLI and the real directory with the recording provider. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: e2e step on Linux on PostgreSQL over TLS with access token shows 3 plan jobs gated and 3 apply jobs only after the apply request. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Complete usability check of the review path. Check: Developer completes the review path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Run full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
