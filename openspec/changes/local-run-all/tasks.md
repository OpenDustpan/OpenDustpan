## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the three-unit e2e test. Check: TestE2ETerragruntLocalRunAllThreeWorkspaces fails on main because the fixture is absent; it calls ANY /api/v2/workspaces/{}/state-versions with an access token and asserts 1 state version plus 1 history entry per workspace.
- [ ] 1.2 Write the lock test. Check: TestE2ETerragruntLocalRunAllLockError fails on main; it calls ANY /api/v2/workspaces/{}/actions/lock with an access token.
- [ ] 1.3 Write the unknown workspace test. Check: TestE2ETerragruntLocalRunAllNotFound fails on main; it calls ANY /api/v2/workspaces/{} with an access token.
- [ ] 1.4 Write the ungranted unit test. Check: TestE2ETerragruntLocalRunAllRefusedUnit fails on main; it calls ANY /api/v2/workspaces/{}/state-versions with an access token.
- [ ] 1.5 Write the shared name test. Check: TestE2ETerragruntLocalRunAllSharedName fails on main; it uses parallelism 3 and no lock timeout.
- [ ] 1.6 Write the no change order check. Check: TestE2ETerragruntLocalRunAllNoOrder fails on main because the zero-order assertion is absent; the per-workspace part calls GET /api/dustpan/v1/namespaces/{}/units with an access token.

## 2. Wiring and docs

- [ ] 2.1 Write the Terragrunt guide section for local use. Check: The docs page command block runs as written in a clean checkout.
- [ ] 2.2 Write the page that says why profiles and blueprints stay the recommended path. Check: The NEW docs page on profiles and blueprints as the recommended path renders and links from the Terragrunt guide.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the usability check for the local path. Check: A developer completes the run from the docs page alone; each error names one command.
- [ ] 4.2 Run the full check suite. Check: `make check` reports zero findings.
