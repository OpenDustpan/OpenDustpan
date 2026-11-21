## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing history test for parent pointer and claim ID on merged versions in pkg/inventory/inventorytest/mem_test.go (func TestHistoryConformance is there). TestHistoryShowsClaimPerMergedVersion is NEW. Check: TestHistoryShowsClaimPerMergedVersion fails on main.
- [ ] 1.2 Add failing audit test for one merged entry with each claim ID in the same transaction in NEW file pkg/audit/merge_record_test.go. TestMergedAuditEntry is NEW. Check: TestMergedAuditEntry fails on main.
- [ ] 1.3 Add failing sealed values test for merges from two parents with masked reads and unsealed refusal in NEW file pkg/audit/merge_sealed_test.go. TestMergeKeepsSealedValues is NEW. Check: TestMergeKeepsSealedValues fails on main.

- [ ] 1.4 SECURITY. Add a hostile handler test for the HTTP handler authorization layer in NEW file pkg/audit/merge_handler_authz_test.go. The test reads the state version list and the merged audit entry without the read actions and expects 403 for each. TestMergedHistoryAndAuditDenyWithoutReadAction is NEW. Check: TestMergedHistoryAndAuditDenyWithoutReadAction fails on main.

## 2. Build

- [ ] 2.1 Store parent pointer and claim ID in the baseline in name order and expose them in the state version list. Edit the baseline in NEW file pkg/audit/merge_record.go for the record fields. Edit the route declaration and the adapter for `ANY /api/v2/workspaces/{}/state-versions` in the files that main declares for it. Check: `ANY /api/v2/workspaces/{}/state-versions` shows parent and claim per version with declared read action.
- [ ] 2.2 Build mergeRecord through newMergeRecord (NEW) in NEW file pkg/audit/merge_record.go and write the merged audit entry in the same fenced transaction. Edit the route declaration and the adapter for `ANY /api/audit/entries` in the files that main declares for it. Check: `ANY /api/audit/entries` shows the merged entry with declared read action and no plain secrets.

## 3. Definition of done

- [ ] 3.1 Add e2e step in NEW file e2e/concurrent_applies_merge_history_audit_test.go with fake cloud provider log for two concurrent applies and history check via `ANY /api/v2/workspaces/{}/state-versions` with an access token. Check: make e2e with real OpenTofu shows overlapping provider periods, final state with both changes, two versions with claims, and no lost resource.
- [ ] 3.2 Write the docs page for merge history and audit in NEW file docs/guides/merge-history-and-audit.md. Check: Docs page for merge history renders and each command runs as written.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 3.3 Agent pre-run (human run-through in person-steps (concurrent-applies-merge-history-audit 3.3)): Run usability check of the history and audit path via `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: Developer completes history check from the docs page alone. Record the transcript in the pull request body.
- [ ] 3.4 Run wiring, route, e2e, docs, and usability checks with make check, covering `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: make check passes with zero findings.
