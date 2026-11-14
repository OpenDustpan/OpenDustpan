## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the three-unit e2e test in the NEW file `e2e/terragrunt_local_test.go`. It adds the NEW three-unit Terragrunt fixture in `e2e/testdata/terragrunt-local/`. The test runs the real `terragrunt run-all` through fetcher.Install(ctx, e2e.Terragrunt(), version), with the OpenTofu binary of the fetcher as its TG_TF_PATH; no test calls tofu directly in place of Terragrunt. Check: TestE2ETerragruntLocalRunAllThreeWorkspaces fails on main because the fixture is absent; it calls ANY /api/v2/workspaces/{}/state-versions with an access token and asserts 1 state version plus 1 history entry per workspace.
- [ ] 1.2 Write the lock test in the NEW file `e2e/terragrunt_local_test.go`. Check: TestE2ETerragruntLocalRunAllLockError fails on main; it calls ANY /api/v2/workspaces/{}/actions/lock with an access token.
- [ ] 1.3 Write the unknown workspace test in the NEW file `e2e/terragrunt_local_test.go`. Check: TestE2ETerragruntLocalRunAllNotFound fails on main; it calls ANY /api/v2/workspaces/{} with an access token.
- [ ] 1.4 Write the ungranted unit test in the NEW file `e2e/terragrunt_local_test.go`. Check: TestE2ETerragruntLocalRunAllRefusedUnit fails on main; it calls ANY /api/v2/workspaces/{}/state-versions with an access token.
- [ ] 1.5 Write the shared name test in the NEW file `e2e/terragrunt_local_test.go`. Check: TestE2ETerragruntLocalRunAllSharedName fails on main; it uses parallelism 3 and no lock timeout, and calls ANY /api/v2/workspaces/{}/state-versions with an access token.
- [ ] 1.6 Write the no change order check in the NEW file `e2e/terragrunt_local_test.go`. Check: TestE2ETerragruntLocalRunAllNoOrder fails on main because the zero-order assertion is absent; the per-workspace part calls GET /api/dustpan/v1/namespaces/{}/units with an access token.

## 2. Wiring and docs

- [ ] 2.0 Add NEW func Terragrunt() Tool and the NEW constant ToolTerragrunt = "terragrunt" in the existing file `e2e/tool.go`, next to the pinned OpenTofu tool. Use version 1.2.1 (released 2026-10-23) as the one default and the one pinned version. Set ReleaseURL to https://github.com/gruntwork-io/terragrunt/releases/download/v%[1]s/%[2]s, the archive to terragrunt_<goos>_<arch>.tar.gz (no version in the name, so one pinned version only) and versionsEnv to E2E_TERRAGRUNT_VERSIONS. Pin these sha256 sums from SHA256SUMS of the GitHub release v1.2.1: terragrunt_linux_amd64.tar.gz 08c8844cc69e4f03451ebb8f347cb57d9bd6b8db7b16e437ae5ec6f001228ef9, terragrunt_linux_arm64.tar.gz 8b92d56a20b2848365e22ef94f963fbfe25fedeebcacffacbc286cfcf9966066, terragrunt_darwin_amd64.tar.gz 29806d1708f5c54e54bc6adfcacfb710d4bb1f6f180efd751692e6fad5985640, terragrunt_darwin_arm64.tar.gz 0aded323f844ce6567a15feb26393b67eff1400dc1722203fa4dd4e23b3bb182. Add the version to the version list in `e2e/doc.go`. Add the test in the existing file `e2e/tool_test.go`. This task has no e2e step: it is a unit test of the fetcher. Check: the test checks the pinned sums of each platform and the one version, and a download with a wrong sum is refused.
- [ ] 2.1 Write the section "Run Terragrunt locally" of the NEW guide `docs/guides/terragrunt.md`, in the layout of the existing guides (for example `docs/guides/run-triggers.md`), and link it from the existing `docs/guides/quickstart.md`. Check: The docs page command block runs as written in a clean checkout.
- [ ] 2.2 Write the NEW explanation page `docs/explanation/profiles-and-blueprints.md`, in the layout of the existing `docs/explanation/security-model.md`, which says why profiles and blueprints stay the recommended path; link it from `docs/guides/terragrunt.md` and `docs/guides/publish-a-blueprint.md`. Check: `make docs-lint` passes and both links resolve.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the usability check for the local path. Check: A developer completes the run from the docs page alone; each error names one command.
- [ ] 4.2 Run the full check suite. Check: `make check` reports zero findings.
