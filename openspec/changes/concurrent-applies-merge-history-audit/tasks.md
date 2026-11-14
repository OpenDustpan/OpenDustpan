## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing history test for parent pointer and claim ID on merged versions. Check: TestHistoryShowsClaimPerMergedVersion fails on main.
- [ ] 1.2 Add failing audit test for one merged entry with each claim ID in the same transaction. Check: TestMergedAuditEntry fails on main.
- [ ] 1.3 Add failing sealed values test for merges from two parents with masked reads and unsealed refusal. Check: TestMergeKeepsSealedValues fails on main.

## 2. Build

- [ ] 2.1 Store parent pointer and claim ID in the baseline in name order and expose them in the state version list. Check: `ANY /api/v2/workspaces/{}/state-versions` shows parent and claim per version with declared read action.
- [ ] 2.2 Build mergeRecord through newMergeRecord and write the merged audit entry in the same fenced transaction. Check: `ANY /api/audit/entries` shows the merged entry with declared read action and no plain secrets.

## 3. Definition of done

- [ ] 3.1 Add e2e step with fake cloud provider log for two concurrent applies and history check via `ANY /api/v2/workspaces/{}/state-versions` with an access token. Check: make e2e with real OpenTofu shows overlapping provider periods, final state with both changes, two versions with claims, and no lost resource.
- [ ] 3.2 Write the docs page for merge history and audit. Check: Docs page for merge history renders and each command runs as written.
- [ ] 3.3 Run usability check of the history and audit path via `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: Developer completes history check from the docs page alone.
- [ ] 3.4 Run wiring, route, e2e, docs, and usability checks with make check, covering `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: make check passes with zero findings.
