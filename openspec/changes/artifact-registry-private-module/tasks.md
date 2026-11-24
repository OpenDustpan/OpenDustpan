## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Not applicable: tests of module publish accept and refuse exist on main in (read only) `internal/registry/service/module_test.go` (TestPublishModuleThenRead, TestPublishModuleRefusals). Check: the pull request body names each test and its run result.
- [x] 1.2 Not applicable: tests of module discovery, version list and download exist on main in (read only) `internal/api/tfe/registry_modules_test.go` (TestModuleVersions, TestModuleDownload). Check: the pull request body names each test and its run result.
- [x] 1.3 Not applicable: tests of cross-namespace, wrong host, bad name, missing version and duplicate publish exist on main in (read only) `internal/api/tfe/registry_modules_test.go` (TestModuleHostGate, TestModuleMissing, TestModuleAuthorization) and (read only) `internal/registry/service/module_test.go` (TestPublishModuleRepeatAndConflict). Check: the pull request body names each test and its run result.

## 2. Build

- [x] 2.1 Not applicable: module publish checks and module serve routes with namespace grants and audit exist on main in (read only) `internal/registry/service/module.go` line 54 and (read only) `internal/api/tfe/registry_modules.go` line 49. Check: the pull request body lists the differences found in task 0.1, or says that there are none.
- [x] 2.2 Not applicable: the routes are wired and the e2e step exists on main in (read only) `e2e/registry_module_test.go` line 109 (TestRegistryModuleTofuInit): the e2e step `tofu init` with an access token calls GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions on the real binary and installs the private module. Check: the pull request body names the test.
- [x] 2.3 Add the test that runs each command of the section "Publish a module" of `docs/guides/private-registry.md` as written, in NEW file e2e/artifact_registry_private_module_docs_test.go, and fix each command in the guide that fails. Check: e2e step: TestPrivateModuleGuideCommands parses the sh blocks of the section "Publish a module" and sends the request of each command to a test server, with the host, namespace, token, Idempotency-Key and archive substituted; a changed command in the guide fails the test. The test sends PUT /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions/{} with an access token, and `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [x] 4.1 Confirm wiring, route actions, e2e, docs, audit entries, and usability. Check: Route GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions with an access token passes e2e on SQLite and PostgreSQL; routes declare authorization actions; usability check passes; `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only)
