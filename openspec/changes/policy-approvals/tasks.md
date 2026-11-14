## 1. Failing tests first

- [ ] 1.1 Add failing tests for destroy approval fresh step, token refusal and no-factor refusal with enrollment text Check: TestDestroyApprovalNeedsFreshStep fails on main, passes after fix
- [ ] 1.2 Add failing composed e2e and service tests for the step-6 path through runner jobs with owning 403 and 409 cases Check: TestPolicyApprovalsComposedRun fails on main, passes after fix

## 2. Definition of done

- [ ] 2.1 Wire job start, approve and apply with authorization actions and audit entries Check: e2e step for milestone 5 step 6 exercises job start, approve and apply routes with the owning 403 and 409 cases
- [ ] 2.2 Add e2e step for step 6 of milestone 5 exit test Check: e2e step on SQLite and PostgreSQL through real binary over TLS with real Pulumi CLI and OpenTofu
- [ ] 2.3 Write docs page for the destroy fresh step and the composed step-6 path Check: docs page runbook commands run as written
- [ ] 2.4 Run usability check Check: Namespace administrator completes path from docs page alone; each error names one command
- [ ] 2.5 Run full check Check: `make check` with zero findings
