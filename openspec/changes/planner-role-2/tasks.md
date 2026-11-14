## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing service test for planner plan start. Check: TestPlanAsPlannerStartsJob fails on main before the fix and passes after.
- [ ] 1.2 Add failing service test for planner apply refusal. Check: TestApplyAsPlannerGives403 fails on main before the fix and passes after.
- [ ] 1.3 Add failing service test for planner destroy refusal. Check: TestDestroyAsPlannerGives403 fails on main before the fix and passes after.
- [ ] 1.4 Add failing service tests for secrets by pinned SHA. Check: TestPlannerSecretsByPinnedSHA fails on main before the fix and passes after.
- [ ] 1.5 Add failing service test for planner plan read. Check: TestPlannerReadsMaskedPlan fails on main before the fix and passes after.
- [ ] 1.6 Add failing access test for changeorder:author through the stored planner grant. Check: TestPlannerAuthorsChangeOrder fails on main before the fix and passes after.
- [ ] 1.7 Add failing service test for grant removed after queue. Check: TestPlannerGrantRemovedKeepsJobRefusesReads fails on main before the fix and passes after.
- [ ] 1.8 Add failing service test for audit in the same transaction. Check: TestPlannerAuditInSameTransaction fails on main before the fix and passes after.
- [ ] 1.9 Add failing service test for unknown operation refusal. Check: TestUnknownOperationGives400 fails on main before the fix and passes after.
- [ ] 1.10 Add the e2e step for plan success, masked read, apply 403 and destroy 403. Route: POST /api/dustpan/v1/namespaces/{namespace}/jobs. Credential: personal access token of a planner principal. BLOCKED by the real runner pool e2e of the runners items listed in depends_on. Check: The e2e step fails on main before the fix and passes after, once the runner e2e exists.

## 2. Implementation

- [ ] 2.1 Wire operation-to-action check in job start path. Check: Route test lists POST jobs with unit:run-plan for plan, the apply action for apply, the destroy action for destroy, and 400 for unknown operations.
- [ ] 2.2 Record job decision and audit entry together. Check: Audit entry names starter, operation and decision in the same transaction as the queue insert.
- [ ] 2.3 Write docs page for the planner role with the workstation caveat. Check: Docs page runs as written and warns that workstation plans read state with state actions outside this role.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run full check suite. Check: `make check` reports zero findings.
- [ ] 4.2 Run the e2e step on both stores. Check: E2E step on POST /api/dustpan/v1/namespaces/{namespace}/jobs with a personal access token of a planner principal runs against the real server over TLS on SQLite and PostgreSQL.
- [ ] 4.3 Complete usability check. Check: Persona completes plan from docs page alone.
