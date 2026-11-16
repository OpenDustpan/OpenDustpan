## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Find every fresh-step call site on current main with `rg -n 'sessionauth\.Fresh\(|RequireFresh\(|requireFresh\(|requireFreshStep\(|checkTokenFreshness|NeedsFreshStep\(|newDestroyFresh|checkDestroyProof|freshAuth\(|FreshAuthSeconds|FreshAuthForApprovals' --glob '*.go' -g '!*_test.go' .`, and write the list into the pull request body with one line for each: gated, or kept by ADR 385 decision 5 with the decision. Add each site that open pull requests added (for example the trust rule check and the session-limit check). Check: the list is in the pull request body, and `rg` finds no site that the list omits.
- [ ] 1.2 Test the default and the source: TestFreshAuthEnabledDefault in pkg/setting and internal/api/server/settings_test.go. Check: fails on main
- [ ] 1.3 Test Required fails closed: TestPolicyRequiredFailsClosed in pkg/freshstep/freshstep_test.go (a setting store that returns an error gives true). Check: fails on main
- [ ] 1.4 Test each layer with the setting off and on, as one table for each layer: store (checkDestroyProof), service (job, accesstoken, profile, setting), handler (server, tfe, pulumi, profile, issuer). The off rows pass with no step, and the on rows get 403 `mfa.fresh-step-required`. Each guard shows a test that fails when the guard is removed. Check: the off rows fail on main
- [ ] 1.5 Test TestDestroyApprovalByTokenFollowsSetting: an access token approves a `destroy` with the setting off, and is refused with it on. Check: fails on main
- [ ] 1.6 Test TestFreshAuthEnabledChangeNeedsStep: on to off needs a step, off to on needs none, both write an audit entry with the old and the new value. Check: fails on main
- [ ] 1.7 Test TestStoredRulesShowNotEnforced: a rule with `freshAuthSeconds` and a namespace with `freshAuthForApprovals` show `"enforced": false` with the setting off, and keep their stored value. Check: fails on main
- [ ] 1.8 Test TestEveryFreshCallSiteUsesPolicy: no file outside pkg/freshstep and the wiring calls a bare freshness check (an `rg`-based test of the repository). Check: fails on main
- [ ] 1.9 Test TestStepsThatStayAreNotGated: the first-administrator code, the factor change and reset, the enrollment code and the break-glass proof ask as before with the setting off. Check: passes on main and keeps passing

## 2. Implementation

- [ ] 2.1 Add pkg/freshstep (`Policy`, `Guard`, `NewGuard`) with the contract of design.md, and its leak_test.go. Check: `go test ./pkg/freshstep/...`
- [ ] 2.2 Declare `freshAuthEnabled` in the settings registry (installation scope, boolean, default false, reduces security on the change to off) and make `pkg/setting/change.go` treat on to off as a weaker change. Check: `go test ./pkg/setting/...`
- [ ] 2.3 Make the one `Policy` in pkg/dustpan and give each service and each handler of the list of 1.1 the same `Guard`. Check: `go test ./pkg/... ./internal/...` for the packages of the list
- [ ] 2.4 Make the destroy approval read the policy in pkg/job, internal/api/jobs/approve.go and the store (`checkDestroyProof`). With the setting off, the approval records `auth: none`. Check: TestDestroyApprovalByTokenFollowsSetting passes
- [ ] 2.5 Add `enforced` to the rule API and the namespace API, and regenerate the API description and the clients. Check: `make generate` and `make clients-check` are clean
- [ ] 2.6 Write the audit entry of the change of `freshAuthEnabled` with the old and the new value. Check: TestFreshAuthEnabledChangeNeedsStep passes

## 2b. Side effects (computed from main)

- [ ] 2b.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change. The files that this change edits are the ones in the Files list of proposal.md.

## 3. Definition of done

- [ ] 3.1 Each site of 1.1 has a test with the setting off and on. Check: the tests of 1.4 pass
- [ ] 3.2 Route keeps its authorization actions. Check: Route test passes
- [ ] 3.3 E2E step: PATCH `/api/server/settings` for `freshAuthEnabled`, then approve a `destroy` with an access token with the setting off and on. Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 In the guides named in the Files list, state the setting, its default, that off means an access token with the grant may approve a `destroy` and make a setting weaker, and that `enforced` shows a stored rule that is not in force. Check: make docs-lint
- [ ] 3.5 Usability check from the docs page alone. Check: Site administrator turns the setting on and off from the docs page. A person's step.
- [ ] 3.6 Run make check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
