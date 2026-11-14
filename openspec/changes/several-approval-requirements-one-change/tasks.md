## 1. Failing tests

- [ ] 1.1 Add failing matcher tests for two requirements, users, owners, fresh window, unless, and maximum matching Check: TestApproval_BothRequirementsNeedDistinctPeople fails on main
- [ ] 1.2 Add failing maximum-matching test where only a non-greedy assignment satisfies both requirements Check: TestApproval_GreedyOrderStillSatisfies fails on main
- [ ] 1.3 Add failing handler test for the 403 body that names each unmet requirement and missing count Check: TestApproval_RefusalNamesMissingRequirement fails on main
- [ ] 1.4 Add failing service tests for eligibility recheck, revocation, self-approval, plan binding, withdrawal, dedupe, and window enforcement at approval and apply time Check: TestApproval_FreshWindowRefusesOldSignIn fails on main

## 2. Definition of done

- [ ] 2.1 Extend decider to maximum bipartite matching of distinct approvers across requirements Check: go test ./pkg/gate/... passes
- [ ] 2.2 Wire eligibility, fresh window, unless, plan binding, and audit through job and change-order approve routes Check: POST apply with one unmet requirement returns the named 403 body
- [ ] 2.3 Add e2e step against the real CLI binaries Check: e2e job with namespace plus collection rule applies only after both approvals
- [ ] 2.4 Write docs page for several requirements and distinct approvers Check: Docs page command runs as written
- [ ] 2.5 Run usability check and full checks Check: `make check` reports zero findings
