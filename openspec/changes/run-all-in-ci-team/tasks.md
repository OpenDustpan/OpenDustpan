## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing e2e test for 3 units applied from one CI job with an access token over the same per-workspace backend as local-run-all; BLOCKED by the per-workspace backend route named in local-run-all. Check: TestTerragruntCiThreeUnitsApply fails on main; BLOCKED by the per-workspace backend route named in local-run-all, uses access token
- [ ] 1.2 Write the failing check that a CI run-all over 3 units leaves no change order and no discovery job; BLOCKED by the per-workspace backend route named in local-run-all. Check: TestTerragruntCiCreatesNoChangeOrder fails on main; BLOCKED by the per-workspace backend route named in local-run-all, uses access token
- [ ] 1.3 Write the failing tests for same-workspace address handling, lock refusal and credential refusal, including cross-workspace denial; BLOCKED by the per-workspace backend route named in local-run-all. Check: TestTerragruntCiSameWorkspaceAddress, TestTerragruntCiLockRefusal and TestTerragruntCiCredentialRefusal fail on main; BLOCKED by the per-workspace backend route named in local-run-all, use access token

## 2. Path and docs

- [ ] 2.1 Run the hostile cross-workspace credential check against the per-workspace authorization gate with an access token; BLOCKED by the per-workspace backend route named in local-run-all. Check: The token granted on workspace A is denied on workspace B; BLOCKED by the per-workspace backend route named in local-run-all, uses access token
- [ ] 2.2 Write the team CI section of the Terragrunt guide with non-interactive access-token setup, the per-unit backend block and the note that profiles and blueprints stay the first choice. Check: Docs commands run as written in a clean CI container

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm the wiring reuses the existing per-workspace backend with its authorization actions. Check: The route test passes with the existing per-workspace actions from local-run-all, or the task says BLOCKED by the per-workspace backend route named in local-run-all
- [ ] 4.2 Run the e2e CI form over 3 units against the real binary and real OpenTofu with an access token. Check: `make e2e` Terragrunt CI step passes with an access token, or the step says BLOCKED by the per-workspace backend route named in local-run-all
- [ ] 4.3 Run the usability check from the docs page alone. Check: The person completes the path from the docs page alone and each error names one command
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings
