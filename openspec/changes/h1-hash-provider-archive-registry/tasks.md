## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for h1 computation and SameContent with H1 in NEW file internal/registry/service/publish_h1_test.go. Check: TestPublish_storesH1FromUnpackedBytes fails on main: stored Archive lacks H1.
- [ ] 1.2 Add failing test for manifest-derived protocols and mismatch refusal in NEW file internal/registry/service/publish_protocols_test.go. Check: TestPublish_refusesProtocolMismatch fails on main: stated protocols win over manifest.
- [ ] 1.3 Add failing test for traversal, symlink, size, count, and ratio refusals with 400 registry.bad-archive in NEW file internal/registry/service/publish_archive_test.go. Check: TestPublish_refusesUnsafeArchive fails on main: traversal and zip bomb entries pass unpack.
- [ ] 1.4 Add failing test for missing manifest with and without stated protocols plus malformed manifest in NEW file internal/registry/service/publish_manifest_test.go. Check: TestPublish_refusesStatedProtocolsWithoutManifest fails on main: stated protocols store without manifest.
- [ ] 1.5 Add failing test for registry slug of each namespace and for rename stability in NEW file internal/registry/service/slug_test.go. Check: TestRegistry_servesNamespaceUnderSlug fails on main: unsafe name breaks the registry URL.
- [ ] 1.6 Add failing hostile test for handler 403 then 415 then 413 order without unpack in NEW file internal/api/registry/handlers_publish_test.go. Check: TestPublish_handlerGuards fails on main: oversize and wrong type reach unpack.
- [ ] 1.7 Add failing hostile test for service exact-set protocol match and fail-closed missing manifest in NEW file internal/registry/service/publish_hostile_test.go. Check: TestPublish_serviceProtocolHostile fails on main: false protocols store.

## 2. Build

- [ ] 2.1 Build bounded unpack, H1 computation, and manifest protocol check in NEW file internal/registry/service/unpack.go, with H1 on registry.Archive in pkg/registry/registry.go. Check: `go test -race ./pkg/registry/... ./internal/registry/...` passes with new unpack limits, path checks, and manifest parse.
- [ ] 2.2 Wire H1 and slug through store encode in pkg/store/internal/registrystore/registrystore.go:109, SameContent in pkg/registry/registry.go:397, missing-H1 backfill, and publish path in internal/registry/service/publish.go:307. Check: `go test -race ./pkg/store/...` passes; republish with changed H1 returns 409 registry.content-changed inside one transaction.
- [ ] 2.3 Wire publish route POST /api/dustpan/v1/namespaces/{}/registry/uploads (declaration file internal/api/registry/handlers.go, adapter file NEW internal/api/registry/uploads.go) with unit:manage action on the namespace object and e2e step against the real CLI. Check: `make e2e` registry step for POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token that carries unit:manage on the namespace passes with tofu init against a namespace outside the safe subset, including mismatch and bad-archive cases.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write docs page for publish with h1, slug, and protocol errors. Check: Docs page commands run as written for publish, slug lookup, and protocol error.
- [ ] 4.2 Run usability check of the full publish and install path. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command.
- [ ] 4.3 Pass definition of done for the publish route: wiring, route, e2e step, docs page, usability check, make check. Check: `make check` reports zero findings; wiring uses POST /api/dustpan/v1/namespaces/{}/registry/uploads with unit:manage on the namespace and an access token credential.
