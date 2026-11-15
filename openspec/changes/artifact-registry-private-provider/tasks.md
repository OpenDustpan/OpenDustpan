## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Publish, refusals and keys exist on main: `internal/registry/service/publish_test.go`, `internal/registry/service/refuse_test.go`. Check: `go test ./internal/registry/service/...` passes.
- [x] 1.2 Team protocol exists on main: `internal/api/tfe/registry.go:30`. Check: `go test ./internal/api/tfe/...` passes.
- [ ] 1.3 Add failing tests for the mirror index and cross-namespace isolation in NEW file `internal/registry/service/mirror_test.go`. Check: TestRegistryMirror_IndexListsActiveVersions fails on main.

## 2. Build

- [ ] 2.1 Build the mirror index and version listing from committed active rows in the NEW file `internal/registry/service/mirror.go` next to `internal/registry/service/read.go`. Check: `go test -race ./internal/registry/...` passes.
- [ ] 2.2 Declare the mirror routes `GET /api/dustpan/v1/namespaces/{namespace}/registry/mirror/{addr}/index.json` and `GET /api/dustpan/v1/namespaces/{namespace}/registry/mirror/{addr}/{version}.json` in the existing file `internal/api/registry/routes.go:52` (func Handler.buildRoutes) and serve them in the NEW adapter file `internal/api/registry/mirror.go`, each with its authorization action on the namespace object and an audit entry. Check: `make e2e` runs `tofu init` through the mirror in `e2e/registry_test.go` (NEW test TestRegistryTofuInitMirror, which calls both mirror routes with an access token bearer credential that holds the read grant). Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Run all checks and fix findings in code. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Extend the existing guide docs/guides/private-registry.md with a section on the provider mirror and the one helping command for each error. Check: `make docs-lint` passes.
- [ ] 4.3 Usability check: a Developer completes `tofu init` from docs/guides/private-registry.md alone against the real binary over TLS. Check: the steps in the pull request. A person's step.
