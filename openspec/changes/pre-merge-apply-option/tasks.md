## 1. Failing tests

- [ ] 1.1 Add failing API tests for branch apply on the exact head and default refusal. Check: TestPreMergeApplyStartsFromBranchHead fails on main.
- [ ] 1.2 Add failing test for moved head, older-head approval, and missing approval. Check: TestPreMergeApplyRefusesStaleApproval fails on main.
- [ ] 1.3 Add failing test for branch apply with no plan on the exact head commit. Check: TestPreMergeApplyRefusesMissingPlan fails on main.
- [ ] 1.4 Add failing test for the preMergeGuard re-check before the first change. Check: TestPreMergeApplyStopsOnHeadMoveBeforeFirstChange fails on main.
- [ ] 1.5 Add failing test for the unit settings permission on the setting change. Check: TestPreMergeApplySettingAuth fails on main.

## 2. Definition of done

- [ ] 2.1 Implement the applyTiming value, wiring, route with its authorization actions, and constructor-checked preMergeGuard. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/settings` shows the effective value.
- [ ] 2.2 Add e2e step against the real CLI and the fake forge named by the milestone exit test. Check: Real CLI run against the suite fake GitHub API as forge: branch apply on pre-merge unit succeeds, default unit gets the named refusal.
- [ ] 2.3 Write the docs page in the same pull request. Check: Docs page for the option states the project does not recommend pre-merge apply.
- [ ] 2.4 Run usability check and full checks. Check: `make check` with zero findings.
