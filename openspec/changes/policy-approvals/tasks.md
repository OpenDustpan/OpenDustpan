## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for destroy approval fresh step, token and agent refusal, stale proof, zero and narrower windows, and no-factor refusal with enrollment text Check: TestDestroyApprovalNeedsFreshStep fails on main, passes after fix
- [ ] 1.2 Add failing service and e2e tests for one-approval wait, void on new plan, recheck adds, limit and enforce gate refusals, and stale or digest 409. BLOCKED by the owning changes for the approval route and the session credential; the approval routes are not in the pack, so the route names come from those changes Check: TestPolicyApprovalsComposedRun fails on main, passes after fix

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire job start, approve and apply with authorization actions and audit entries Check: Approve and apply paths return owning 403 mfa.fresh-step-required and 409 cases with audit entries
- [ ] 3.2 Add e2e step for step 6 of milestone 5 exit test. BLOCKED by runners and several-approval-requirements-one-change for the job and approval routes; the credential kinds are a console session for the approval and an access token for the refusal case Check: `make e2e` step for a stack and a workspace on SQLite and PostgreSQL through real binary over TLS with real Pulumi CLI and OpenTofu, covering OpenTofu apply gate refusal
- [ ] 3.3 Write docs page for the destroy fresh step and the composed step-6 path Check: docs page runbook commands run as written
- [ ] 3.4 Run usability check Check: Namespace administrator completes path from docs page alone; each error names one command
- [ ] 3.5 Run full check Check: `make check` with zero findings
