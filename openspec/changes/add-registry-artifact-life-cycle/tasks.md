## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the NEW file `pkg/registry/registry_lifecycle_test.go` for retired pinned install, retired hidden from new resolution, and revoked refused Check: The NEW file `pkg/registry/registry_lifecycle_test.go` with TestRegistryRetiredPinnedInstalls fails on main; No route: the test reads the registry store only.
- [ ] 1.2 Add the NEW file `internal/api/tfe/registry_lifecycle_test.go` for PUT provider and module version state, illegal move, unauthorized denied, and audit entry plus event per change Check: TestRegistryLifeCycleAudit in the NEW file `internal/api/tfe/registry_lifecycle_test.go` fails on main via PUT /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions/{version} with an access token.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Store state per version and enforce retired and revoked on read, list, mirror, and download paths Check: PUT /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions/{version} and PUT /api/dustpan/v1/namespaces/{namespace}/registry/modules/{name}/{system}/versions/{version} with an access token change state with the NEW action registry:manage-versions on the namespace.
- [ ] 3.2 Add e2e step against real OpenTofu through the registry. Confirmed by: CI (e2e job with real OpenTofu) Check: e2e run with real OpenTofu via GET /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions with an access token: pinned retired version installs, revoked version fails with state in CLI message. Confirmed by: CI (e2e job with real OpenTofu)
- [ ] 3.3 Add user docs page for retire and revoke Check: Docs page for registry life cycle renders from docs page alone. Confirmed by: CI (an e2e step in the NEW file e2e/add_registry_artifact_life_cycle_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 3.4 Run full check with zero findings. Confirmed by: CI (make check gate) Check: `make check` reports zero findings. Confirmed by: CI (make check gate)
