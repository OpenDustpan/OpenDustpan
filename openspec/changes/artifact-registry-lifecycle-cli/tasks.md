## 1. Failing tests

- [ ] 1.1 Add failing tests for yank: index omits the version and download still works. Check: TestRegistryYankKeepsDownload fails on main.
- [ ] 1.2 Add failing test for revoke: download answers 409 with code registry.version-revoked. Check: TestRegistryRevokeBlocksDownload fails on main.
- [ ] 1.3 Add failing tests for release list, release show, and keys list in one namespace. Check: TestRegistryReleaseListShow and TestRegistryKeysList fail on main.

## 2. Build

- [ ] 2.1 Apply yank and revoke as state moves with audit entries. Check: go test ./internal/registrysvc -run TestRegistry -count=1
- [ ] 2.2 Wire routes with read and manage actions and refusal codes. Check: go test ./internal/api/... -run TestRegistry -count=1
- [ ] 2.3 Add CLI read commands for releases and keys. Check: `opendustpan registry release list --help` prints the flags.
- [ ] 2.4 Add e2e step against the real CLI over TLS. Check: e2e step with `tofu init` against a yanked and a revoked provider passes.

## 3. Definition of done

- [ ] 3.1 Run full check, docs page, and usability walk from the docs page alone. Check: `make check` with zero findings.
