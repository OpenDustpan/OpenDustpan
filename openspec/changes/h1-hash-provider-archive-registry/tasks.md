## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Add failing test for h1 computation and SameContent with H1 in NEW file internal/registry/service/publish_h1_test.go. Check: TestPublish_storesH1FromUnpackedBytes fails on main: stored Archive lacks H1.
- [x] 1.2 Add failing test for manifest-derived protocols and mismatch refusal in NEW file internal/registry/service/publish_protocols_test.go. Check: TestPublish_refusesProtocolMismatch fails on main: stated protocols win over manifest.
- [x] 1.3 Add failing test for traversal, symlink, size, count, and ratio refusals with 400 and the NEW error code registry.bad-archive in NEW file internal/registry/service/publish_archive_test.go. Check: TestPublish_refusesUnsafeArchive fails on main: traversal and zip bomb entries pass unpack.
- [x] 1.4 Add failing test for a missing manifest, refused with and without stated protocols (registry.ErrInvalid, as the row "none" of testBadProtocols in pkg/registry/registrytest/registrytest.go:570 wants), plus malformed manifest in NEW file internal/registry/service/publish_manifest_test.go. Check: TestPublish_refusesStatedProtocolsWithoutManifest fails on main: stated protocols store without manifest.
- [x] 1.6 Add failing hostile test for handler 403 then 415 then 413 order without unpack in NEW file internal/api/registry/handlers_publish_test.go. Check: TestPublish_handlerGuards fails on main: oversize and wrong type reach unpack.
- [x] 1.7 Add failing hostile test for service exact-set protocol match and fail-closed missing manifest in NEW file internal/registry/service/publish_hostile_test.go. Check: TestPublish_serviceProtocolHostile fails on main: false protocols store.

## 2. Build

- [x] 2.1 Build bounded unpack, H1 computation, and manifest protocol check in NEW file internal/registry/service/unpack.go, with H1 on registry.Archive in pkg/registry/registry.go. Check: `go test -race ./pkg/registry/... ./internal/registry/...` passes with new unpack limits, path checks, and manifest parse.
- [x] 2.2 Wire H1 through store encode in pkg/store/internal/registrystore/registrystore.go:109, SameContent in pkg/registry/registry.go:397, and publish path in internal/registry/service/publish.go:307. Check: `go test -timeout 60m -race ./pkg/store/...` passes; republish with changed H1 returns 409 registry.version-exists inside one transaction.
- [x] 2.4 Add the NEW error code registry.bad-archive (RegistryBadArchive) to internal/errcode/catalog_registry.go, and map the NEW sentinel registry.ErrBadArchive to 400 registry.bad-archive in the error table of internal/api/registry/registry.go (:157). Add the golden line of the NEW code to internal/errcode/testdata/catalog/registry.txt (in its sorted place). Check: `go test ./internal/errcode/... ./internal/api/registry/...` passes
- [ ] 2.3 Wire publish route POST /api/dustpan/v1/namespaces/{}/registry/uploads (declaration file internal/api/registry/handlers.go, adapter file NEW internal/api/registry/uploads.go) with unit:manage action on the namespace object and e2e step against the real CLI. Check: `make e2e` registry step for POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token that carries unit:manage on the namespace passes with tofu init against a namespace inside the safe subset, including mismatch and bad-archive cases. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [x] 3.2 Set H1 in each existing literal of registry.Archive: the new field of task 2.1 makes exhaustruct fail in each literal on main, so each one sets `H1` (the H1 of its archive, or the empty value with a comment where the literal is a fixture that no install reads). The files: `internal/api/tfe/registry_test.go`, `internal/registry/releasecheck/releasecheck.go`, `internal/registry/service/module.go`, `internal/registry/service/refused_audit_test.go`, `pkg/registry/registry_test.go`, `pkg/registry/registrytest/registrytest.go`, `pkg/store/internal/blobrefs/blobrefs_test.go`, `pkg/store/storetest/stored_blob_backup.go`. No lint exception and no linter suppression comment. Check: `golangci-lint run ./...` reports no exhaustruct finding, and `rg -n 'registry\.Archive\{' pkg internal` lists only files that this task names or that the change adds.

## 4. Definition of done

- [x] 4.1 Write docs page for publish with h1 and protocol errors in NEW file docs/guides/registry-publish.md. Check: Docs page commands run as written for publish and protocol error. Confirmed by: CI (an e2e step in the NEW file e2e/h1_hash_provider_archive_registry_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.2 Run usability check of the full publish and install path. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command. A person's step.
- [ ] 4.3 Pass definition of done for the publish route: wiring, route, e2e step, docs page, usability check, make check. Check: `make check` reports zero findings; wiring uses POST /api/dustpan/v1/namespaces/{}/registry/uploads with unit:manage on the namespace and an access token credential. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
