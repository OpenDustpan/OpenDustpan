## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing tests for yank: index omits the version and download works, in NEW file internal/registrysvc/yank_test.go. Check: TestRegistryYankKeepsDownload fails on main.
- [ ] 1.2 Add failing test for revoke: download answers 409 with code registry.version-revoked, in NEW file internal/registrysvc/revoke_test.go. Check: TestRegistryRevokeBlocksDownload fails on main.
- [ ] 1.3 Add failing tests for release list, release show, and keys list, in NEW file internal/registrysvc/read_test.go. Check: TestRegistryReleaseListShow and TestRegistryKeysList fail on main.

## 2. Build

- [ ] 2.1 Apply yank and revoke as state moves with a reason and an audit entry, in NEW file internal/registrysvc/lifecycle.go. Check: go test ./internal/registrysvc -run TestRegistry -count=1
- [ ] 2.2 Wire yank and revoke routes with the manage action and refusal codes, declared in NEW file internal/api/registry/registry_lifecycle_routes.go with the adapter in NEW file internal/api/registry/registry_lifecycle_handler.go. The task adds the NEW top-level test `TestRegistryYankRevoke` in a `_test.go` file of `internal/api`, so that the Check runs it. Check: go test ./internal/api/... -run TestRegistryYankRevoke -count=1
- [ ] 2.3 Wire list and show routes with the read action and the include flag, declared in NEW file internal/api/registry/registry_read_routes.go with the adapter in NEW file internal/api/registry/registry_read_handler.go. The task adds the NEW top-level test `TestRegistryList` in a `_test.go` file of `internal/api`, so that the Check runs it. Check: go test ./internal/api/... -run TestRegistryList -count=1
- [ ] 2.4 Add CLI read commands and yank and revoke wrappers in NEW file cmd/opendustpan/registry.go. Check: `opendustpan registry release list --help` prints the include flag.
- [ ] 2.5 Add e2e steps with real tofu and the Pulumi CLI against GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} over TLS, with a personal token that holds the manage grant, in NEW file e2e/registry_lifecycle_test.go. Check: make e2e passes for a yanked pin and a revoked pin. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.6 Add the registry life cycle docs page in NEW file docs/registry-lifecycle.md. Check: The page lists yank, revoke, list, show, and keys list commands and make docs-lint passes.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done
