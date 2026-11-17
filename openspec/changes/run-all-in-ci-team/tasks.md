## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing e2e test in NEW file e2e/terragrunt_ci_test.go for 3 units applied from one CI job with an access token over the same per-workspace backend as local-run-all. The test calls GET /api/v2/state-versions?filter[workspace][name]=...&filter[organization][name]=... (the route that the TFE-compatible API serves, internal/api/tfe/states_test.go:213 and :646) with an access token. Check: TestTerragruntCiThreeUnitsApply fails on main; uses access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Write the failing check in NEW file e2e/terragrunt_ci_test.go that a CI run-all over 3 units leaves no change order and no discovery job. The test calls GET /api/v2/state-versions?filter[workspace][name]=...&filter[organization][name]=... (the route that the TFE-compatible API serves, internal/api/tfe/states_test.go:213 and :646) with an access token. Check: TestTerragruntCiCreatesNoChangeOrder fails on main; uses access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.3 Write the black-box tests (package tfe_test, an access token against the per-workspace routes) in NEW file internal/api/tfe/terragrunt_ci_test.go for same-workspace address handling, lock refusal and credential refusal, including cross-workspace denial. Check: TestTerragruntCiSameWorkspaceAddress, TestTerragruntCiLockRefusal and TestTerragruntCiCredentialRefusal pass, and each one fails when its guard is removed; use access token

## 2. Path and docs

- [ ] 2.1 Run the hostile cross-workspace credential check in internal/api/tfe/terragrunt_ci_test.go (NEW in task 1.3) against the per-workspace authorization gate with an access token. Check: The token granted on workspace A is denied on workspace B; uses access token
- [ ] 2.2 No route: the test checks the page text and the commands it names only. Write the team CI section of the Terragrunt guide in NEW file docs/guides/terragrunt.md with non-interactive access-token setup, the per-unit backend block and the note that profiles and blueprints stay the first choice. Check: Docs commands run as written in a clean CI container. Confirmed by: CI (an e2e step in the NEW file e2e/run_all_in_ci_team_docs_test.go checks the page text and the commands it names, as e2e/install_docs_test.go does; the page names no route). `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Confirm the wiring reuses the existing per-workspace backend with its authorization actions. Check: The route test passes with the existing per-workspace actions from local-run-all
- [ ] 4.2 Run the e2e CI form over 3 units against the real binary and real OpenTofu with an access token. The test calls GET /api/v2/state-versions?filter[workspace][name]=...&filter[organization][name]=... (the route that the TFE-compatible API serves, internal/api/tfe/states_test.go:213 and :646) with an access token. Check: `make e2e` Terragrunt CI step passes with an access token. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the usability check from the docs page alone. Check: The person completes the path from the docs page alone and each error names one command. A person's step.
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
