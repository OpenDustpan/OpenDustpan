## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing tests for yank: index omits the version and download works. Check: TestRegistryYankKeepsDownload fails on main.
- [ ] 1.2 Add failing test for revoke: download answers 409 with code registry.version-revoked. Check: TestRegistryRevokeBlocksDownload fails on main.
- [ ] 1.3 Add failing tests for release list, release show, and keys list. Check: TestRegistryReleaseListShow and TestRegistryKeysList fail on main.

## 2. Build

- [ ] 2.1 Apply yank and revoke as state moves with a reason and an audit entry. Check: go test ./internal/registrysvc -run TestRegistry -count=1
- [ ] 2.2 Wire yank and revoke routes with the manage action and refusal codes. Check: go test ./internal/api/... -run TestRegistryYankRevoke -count=1
- [ ] 2.3 Wire list and show routes with the read action and the include flag. Check: go test ./internal/api/... -run TestRegistryList -count=1
- [ ] 2.4 Add CLI read commands and yank and revoke wrappers. Check: `opendustpan registry release list --help` prints the include flag.
- [ ] 2.5 Add e2e steps with real tofu and the Pulumi CLI against GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} over TLS, with a personal token that holds the manage grant. Check: make e2e passes for a yanked pin and a revoked pin.
- [ ] 2.6 Add the registry life cycle docs page. Check: The page lists yank, revoke, list, show, and keys list commands and make docs-lint passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run full check and the usability walk from the docs page alone. Check: `make check` with zero findings.
