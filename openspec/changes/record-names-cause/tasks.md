## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write NEW failing test TestFanOutHistoryEntryStoresCause that creates a discovery job plus one fan-out plan job and asserts the history entry carries discovery job ID, requester stable ID, and fixed reason. Check: NEW TestFanOutHistoryEntryStoresCause, fails on main
- [ ] 1.2 Write NEW failing test TestFanOutAuditEntryStoresCause that runs a fan-out apply job and asserts the audit entry carries discovery job ID, requester stable ID, and fixed reason. Check: NEW TestFanOutAuditEntryStoresCause read via ANY /api/audit/entries with access token, fails on main
- [ ] 1.3 Write NEW failing tests for history and audit cause reads, principal rename, removed principal, service requester, discovery no-cause, non-fan-out omission, re-execution same cause, forged cause ignored, foreign discovery refusal, and partial cause absent. Check: NEW TestFanOutCauseBinding, NEW TestFanOutCauseReads, NEW TestFanOutCauseEdgeCases, fail on main

## 2. Implementation

- [ ] 2.1 Implement causeRef storage on fan-out plan and apply jobs with the NEW constructor and copy the three cause fields into history entry rows and audit entry rows using stable IDs. Check: NEW constructor NewCauseRef plus TestFanOutCauseBinding via ANY /api/audit/entries with access token
- [ ] 2.2 Expose the stored cause fields on history reads and on the existing ANY /api/audit/entries list and ANY /api/audit/entries/{} read with existing grants and no TFE-compatible adapter field. Check: GET ANY /api/audit/entries with access token returns cause fields; history read returns cause under existing unit grant; no new route

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e step that runs terragrunt run-all apply over 3 units on a runner through the real binary over TLS with real OpenTofu and Terragrunt on PostgreSQL with an access token credential, then asserts each of the 3 plan jobs and 3 apply jobs yields 1 history entry and 1 audit entry with the discovery job ID, requester ID, and fixed reason. Check: e2e terragrunt run-all apply over 3 units asserting each plan and apply entry via ANY /api/audit/entries with access token
- [ ] 4.2 Write the docs page that shows how to read the cause fields from history and audit entries for fan-out jobs. Check: docs page for Terragrunt fan-out provenance
- [ ] 4.3 Run wiring, authorization with audit-read grant, usability from the docs page alone, and make check with zero findings. Check: `make check` with zero findings
