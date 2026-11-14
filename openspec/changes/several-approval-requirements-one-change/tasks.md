## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing matcher tests for 2 requirements per change in pkg/gate/jobgate scope, with users, owners, fresh window, unless, and maximum matching Check: TestApproval_BothRequirementsNeedDistinctPeople fails on main
- [ ] 1.2 Add failing maximum-matching test for 2 requirements per change in pkg/gate/jobgate scope where only a non-greedy assignment satisfies both requirements Check: TestApproval_GreedyOrderStillSatisfies fails on main
- [ ] 1.3 Add failing handler test for the 403 body that names each unmet requirement and missing count Check: TestApproval_RefusalNamesMissingRequirement fails on main
- [ ] 1.4 Add failing service tests for 2 approvals per change in job service scope for eligibility recheck, revocation, self-approval, plan binding, withdrawal, dedupe, and window enforcement at approval and apply time Check: TestApproval_FreshWindowRefusesOldSignIn fails on main
- [ ] 1.5 Add failing test that 2 applies for one change within 100 milliseconds allow exactly 1 Check: TestApproval_ConcurrentAppliesOneWinner fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Extend decider to maximum bipartite matching for up to 10 requirements per change in pkg/gate/jobgate scope with distinct approvers across requirements Check: go test ./pkg/gate/... passes
- [ ] 3.2 Wire eligibility, fresh window, unless, plan binding, and audit through existing job approve routes Check: POST apply with one unmet requirement returns the named 403 body
- [ ] 3.3 Add e2e step on the existing route POST /api/dustpan/v1/namespaces/{namespace}/jobs with 2 person sign-ins (console session cookie credential), checked with the real Pulumi CLI binary Check: make e2e: a job posted to /api/dustpan/v1/namespaces/{namespace}/jobs with a namespace plus a collection rule applies only after 2 approvals by 2 persons, each with a console session cookie; the real Pulumi CLI reads the resulting update
- [ ] 3.4 Write docs page for several requirements and distinct approvers Check: Docs page command runs as written
- [ ] 3.5 Run usability check and full checks Check: `make check` reports zero findings
