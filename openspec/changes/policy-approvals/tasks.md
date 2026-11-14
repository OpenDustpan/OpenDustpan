## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests in the NEW file `pkg/dustpan/destroyapproval_test.go` for destroy approval fresh step, token and agent refusal, stale proof, zero and narrower windows, and no-factor refusal with enrollment text. Check: TestDestroyApprovalNeedsFreshStep fails on main, passes after fix
- [ ] 1.2 Add failing service tests in the NEW file `pkg/dustpan/policyapprovals_test.go` and failing e2e tests in the NEW file `e2e/policy_approvals_test.go` for one-approval wait, void on new plan, recheck adds, limit and enforce gate refusals, and stale or digest 409. BLOCKED by the owning changes for the approval route and the session credential; the approval routes are not in the pack, so the route names come from those changes Check: TestPolicyApprovalsComposedRun fails on main, passes after fix. Confirmed by: CI (the agent writes the e2e test).

- [ ] 1.3 SECURITY. Add a hostile test in the NEW file `pkg/dustpan/destroyapproval_test.go` named TestDestroyApprovalServiceRefusesTokenAndStaleProof. It sends a destroy approval straight to the service with an access token, an agent credential, a stale proof and no fresh step. Check: the test gets 403 mfa.fresh-step-required with no signature written; it fails when the service fresh check is removed.

## 2. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 3. Definition of done

- [ ] 3.0 SECURITY. Build the destroy approval fresh check in the NEW file `pkg/dustpan/destroyapproval.go`, with the unexported type destroyFresh and the NEW constructor newDestroyFresh. The check runs in the same transaction as the signature write, uses the shortest window of installation, namespace and rule, and refuses token, agent and no-factor callers with 403 mfa.fresh-step-required. Check: TestDestroyApprovalServiceRefusesTokenAndStaleProof passes and no signature is written on refusal.
- [ ] 3.1 Wire job start, approve and apply with authorization actions and audit entries Check: Approve and apply paths return owning 403 mfa.fresh-step-required and 409 cases with audit entries
- [ ] 3.2 Add e2e step for step 6 of milestone 5 exit test in the NEW file `e2e/policy_approvals_test.go`. It uses the job and approval routes of the owning changes. BLOCKED by runners and several-approval-requirements-one-change for the job and approval routes; the credential kinds are a console session for the approval and an access token for the refusal case Check: `make e2e` step for a stack and a workspace on SQLite and PostgreSQL through real binary over TLS with real Pulumi CLI and OpenTofu, covering OpenTofu apply gate refusal. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Write docs page for the destroy fresh step and the composed step-6 path in the NEW file `docs/guides/policy-approvals.md` Check: docs page runbook commands run as written. Confirmed by: CI (an e2e step in the NEW file e2e/policy_approvals_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 3.4 Run usability check Check: Namespace administrator completes path from docs page alone; each error names one command. A person's step.
- [ ] 3.5 Run full check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
