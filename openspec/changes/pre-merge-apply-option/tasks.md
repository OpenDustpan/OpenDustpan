## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing tests for branch apply on the exact head and default refusal in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplyStartsFromBranchHead fails on main.
- [ ] 1.2 Add failing test for moved head, older-head approval and missing approval in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplyRefusesStaleApproval fails on main.
- [ ] 1.3 Add failing test for no plan on the exact head in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplyRefusesMissingPlan fails on main.
- [ ] 1.4 Add failing test for the re-check before the first change and for a forge failure in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplyStopsOnHeadMoveBeforeFirstChange fails on main.
- [ ] 1.5 Add failing test for two concurrent branch apply requests on one unit in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplyConcurrentRefusedAsLocked fails on main.
- [ ] 1.6 Add failing test for the setting permission in NEW file pkg/dustpan/premergeapply_test.go. Check: TestPreMergeApplySettingAuth fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Implement applyTiming, wiring, the job start route with its authorization actions and preMergeGuard. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units` shows the effective value.
- [ ] 3.2 Add e2e step in NEW file e2e/pre_merge_apply_test.go. The step calls the job start route and uses the access token credential of the fake forge. It is BLOCKED until the route open question fixes the route and the token. Check: BLOCKED by the route open question.
- [ ] 3.3 Write the docs page in NEW file docs/guides/pre-merge-apply.md in the same pull request. Check: Docs page states the project does not recommend pre-merge apply. `make docs-lint` passes on each edited guide.
- [ ] 3.4 Run usability check and full checks. Check: `make check` with zero findings.
