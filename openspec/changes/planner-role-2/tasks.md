## 1. Failing tests

- [ ] 1.1 Add failing service test for planner plan start. Check: TestPlanAsPlannerStartsJob passes and fails on main.
- [ ] 1.2 Add failing service test for planner apply refusal with job.forbidden and missing-action text. Check: TestApplyAsPlannerGives403 passes and fails on main.
- [ ] 1.3 Add failing service test for planner destroy refusal. Check: TestDestroyAsPlannerGives403 passes and fails on main.
- [ ] 1.4 Add failing service tests for masked run on unprotected commit, secret open on protected branch, masked run on branch-check error, and no-secrets unit, with no secret in the log. Check: TestPlannerSecretsByNamedCommit passes and fails on main.
- [ ] 1.5 Add failing service test for planner plan read with no secret value. Check: TestPlannerReadsMaskedPlan passes and fails on main.
- [ ] 1.6 Add failing access test for changeorder:author through the stored planner grant. Check: TestPlannerAuthorsChangeOrder passes and fails on main.
- [ ] 1.7 Add failing service test for grant removed after queue. Check: TestPlannerGrantRemovedKeepsJobRefusesReads passes and fails on main.
- [ ] 1.8 Add failing service test for audit failure after commit. Check: TestPlannerAuditFailureKeepsJob passes and fails on main.
- [ ] 1.9 Add failing service test for unknown operation refusal. Check: TestUnknownOperationGives400 passes and fails on main.
- [ ] 1.10 Add failing e2e step with real runner and real CLI traffic for plan success, masked read, apply 403 and destroy 403. Check: TestE2EPlannerPlanApply passes and fails on main.

## 2. Implementation

- [ ] 2.1 Wire operation-to-action check in job start path. Check: Route test lists POST jobs with unit:run-plan for plan, the apply action for apply, the destroy action for destroy, and 400 for unknown operations.
- [ ] 2.2 Record job decision and audit entry after commit. Check: Audit entry names starter, operation and decision; audit failure never rolls back the job decision.
- [ ] 2.3 Write docs page for the planner role with the workstation caveat. Check: Docs page runs as written and warns that workstation plans read state with state actions outside this role, and names destroy refusal and masked unprotected runs.

## 3. Definition of done

- [ ] 3.1 Run full check suite. Check: `make check` reports zero findings.
- [ ] 3.2 Run e2e step on both stores. Check: E2e step runs against real server over TLS on SQLite and PostgreSQL.
- [ ] 3.3 Complete usability check. Check: Persona completes plan from docs page alone.
