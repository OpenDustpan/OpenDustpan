## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for serve of named base per claim in NEW file pkg/workspace/claim_merge_test.go. Check: TestServeClaimBase_ReturnsNamedBase fails on main
- [ ] 1.2 Add failing test for latest read without claim and pruned-base refusal in NEW file pkg/workspace/claim_merge_test.go. Check: TestReadWithoutClaim_ReturnsLatest fails on main
- [ ] 1.3 Add failing test for per-instance merge of two disjoint uploads in NEW file pkg/workspace/claim_merge_test.go. Check: TestMergeDisjointInstances_NoLostResource fails on main
- [ ] 1.4 Add failing test for root outputs, lineage, version, replacement, and delete conflicts in NEW file pkg/workspace/claim_merge_test.go. Check: TestMergeNonInstanceFields_Refused fails on main
- [ ] 1.5 Add failing test for atomic serial commit, concurrent commits, and crash safety in NEW file pkg/workspace/claim_merge_test.go. Check: TestMergeAtomicCommit_NoHalfState fails on main
- [ ] 1.6 Add failing test for fence, expiry, and foreign-claim refusal in NEW file pkg/workspace/claim_merge_test.go. Check: TestMergeFence_RejectsMismatch fails on main
- [ ] 1.7 Add failing test for each of the six strict cases with 423 in NEW file pkg/workspace/claim_merge_test.go. Check: TestStrictCase_HoldsWholeUnitLock fails on main
- [ ] 1.8 Add failing test for capability retry, force upload fence, and orphan safety in NEW file pkg/workspace/claim_merge_test.go. Check: TestCapabilityRetry_SameContentSucceeds fails on main
- [ ] 1.9 NEW. Write a failing test in NEW file pkg/gate/facts_claims_test.go that an access limit on `ref_protected` refuses an upload under an apply claim from an unprotected branch. Check: NEW test TestStateWriteLimitReadsTokenClaims fails on main

## 2. Merge core

- [ ] 2.1 Serve reads under a claim from the named base version and latest without a claim in NEW file pkg/workspace/claim_merge.go. Check: `go test ./pkg/workspace/... -run TestServeClaimBase -count=1` passes
- [ ] 2.2 Merge uploads one resource instance at a time under fence with conflict refusal in NEW file pkg/workspace/claim_merge.go. Check: `go test ./pkg/workspace/... -run TestMergeDisjoint -count=1` passes
- [ ] 2.3 Enforce refuse rules, empty uploads, fail-closed behavior, and six strict whole-unit cases in NEW file pkg/workspace/claim_merge.go. Check: `go test ./pkg/workspace/... -run TestStrictCase -count=1` passes

## 3. Definition of done

- [ ] 3.1 Wire routes with authorization actions and fence check Check: Route test shows TFE state-version routes with authorization actions and fence check
- [ ] 3.2 Add e2e step with real OpenTofu against the existing routes ANY /api/v2/state-versions and ANY /api/v2/state-uploads/{}/{}, with the job token of each runner job as the credential: owner ADR 369 decision 1 says "A TFE-compatible request names its apply claim through the job token", and main reads it at internal/api/tfe/locks.go:179 (LockForJob with c.p.JobTokenID()) (PM decision, rule 16) Check: E2E step shows two overlapping apply periods in a fake cloud provider log, final state with both changes, and two new state versions. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Write docs page for merge behavior and strict cases in NEW file docs/guides/concurrent-applies-merge.md. Check: Docs page ships in the same pull request
- [ ] 3.4 Run usability check of developer path Check: Usability walk from docs page alone passes with exact errors. A person's step.
- [ ] 3.5 Run full check suite Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
