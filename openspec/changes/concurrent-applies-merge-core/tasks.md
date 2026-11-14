## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for serve of named base per claim Check: TestServeClaimBase_ReturnsNamedBase fails on main
- [ ] 1.2 Add failing test for latest read without claim and pruned-base refusal Check: TestReadWithoutClaim_ReturnsLatest fails on main
- [ ] 1.3 Add failing test for per-instance merge of two disjoint uploads Check: TestMergeDisjointInstances_NoLostResource fails on main
- [ ] 1.4 Add failing test for root outputs, lineage, version, replacement, and delete conflicts Check: TestMergeNonInstanceFields_Refused fails on main
- [ ] 1.5 Add failing test for atomic serial commit, concurrent commits, and crash safety Check: TestMergeAtomicCommit_NoHalfState fails on main
- [ ] 1.6 Add failing test for fence, expiry, and foreign-claim refusal Check: TestMergeFence_RejectsMismatch fails on main
- [ ] 1.7 Add failing test for each of the six strict cases with 423 Check: TestStrictCase_HoldsWholeUnitLock fails on main
- [ ] 1.8 Add failing test for capability retry, force upload fence, and orphan safety Check: TestCapabilityRetry_SameContentSucceeds fails on main

## 2. Merge core

- [ ] 2.1 Serve reads under a claim from the named base version and latest without a claim Check: `go test ./pkg/workspace/... -run TestServeClaimBase -count=1` passes
- [ ] 2.2 Merge uploads one resource instance at a time under fence with conflict refusal Check: `go test ./pkg/workspace/... -run TestMergeDisjoint -count=1` passes
- [ ] 2.3 Enforce refuse rules, empty uploads, fail-closed behavior, and six strict whole-unit cases Check: `go test ./pkg/workspace/... -run TestStrictCase -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire routes with authorization actions and fence check Check: Route test shows TFE state-version routes with authorization actions and fence check
- [ ] 4.2 Add e2e step with real OpenTofu using a job token credential and a fake cloud provider log Check: E2E step shows two overlapping apply periods, final state with both changes, and two new state versions
- [ ] 4.3 Write docs page for merge behavior and strict cases Check: Docs page ships in the same pull request
- [ ] 4.4 Run usability check of developer path Check: Usability walk from docs page alone passes with exact errors
- [ ] 4.5 Run full check suite Check: `make check` passes with zero findings
