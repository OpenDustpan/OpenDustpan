## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for refusal of fan-out apply job start before apply request. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyBlockedBeforeApproval fails on main with access token
- [ ] 1.2 Add failing server test for start of fan-out apply jobs after the apply request for the named revision. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyStartsAfterApproval fails on main with access token
- [ ] 1.3 Add failing server test for missing grant and self approval refusal with audit entry. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyRefusesSelfApproval fails on main with access token
- [ ] 1.4 Add failing server test for void on new revision including running-job mark. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyVoidOnNewRevision fails on main with access token
- [ ] 1.5 Add failing server test for duplicate request, retry after rejection, and runner restart. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyStartsEachUnitOnce fails on main with access token
- [ ] 1.6 Add failing server test for exact 403 code, message text, empty-plan skip, and failed-plan refusal. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: TestFanOutApplyGatedMessage fails on main with access token
- [ ] 1.7 Add failing e2e step with recording test provider over 3 units. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: e2e step with real OpenTofu plus Terragrunt over 3 units checks zero provider apply calls before the apply request with access token

## 2. Build

- [ ] 2.1 Wire the applyGate check into the apply job start path with approve action on the change order. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request with approve action on the change order using access token: start returns 403 before and starts after
- [ ] 2.2 Add the CLI command for the apply request with its own test. Check: NEW command `opendustpan change-order apply-request --id <id>` sends the apply request and shows the stored revision in a test with access token
- [ ] 2.3 Wire the audit entry for the apply request path. Check: Denied and stored apply requests each write one audit entry with principal and revision
- [ ] 2.4 Wire void on new plan revision with running-job mark. Check: New plan revision voids the stored request in the same transaction that publishes the revision
- [ ] 2.5 Write the Terragrunt run-all review gate docs page in the same pull request. Check: Docs page command sequence runs as written

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e step against the real CLI and the real directory with the recording provider. Check: BLOCKED by NEW route POST /api/dustpan/v1/change-orders/{id}/apply-request: e2e step on Linux on PostgreSQL over TLS with access token shows 3 plan jobs gated and 3 apply jobs only after the apply request
- [ ] 4.2 Complete usability check of the review path. Check: Developer completes the review path from the docs page alone and each error names the one command that helps
- [ ] 4.3 Run full check suite. Check: `make check` with zero findings
