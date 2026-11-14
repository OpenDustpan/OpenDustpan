## 1. Failing tests first

- [ ] 1.1 Add failing test for h1 computation and SameContent with H1. Check: TestPublish_storesH1FromUnpackedBytes fails on main: stored Archive lacks H1.
- [ ] 1.2 Add failing test for manifest-derived protocols and mismatch refusal. Check: TestPublish_refusesProtocolMismatch fails on main: stated protocols win over manifest.
- [ ] 1.3 Add failing test for traversal, symlink, size, count, and ratio refusals with 400 registry.bad-archive. Check: TestPublish_refusesUnsafeArchive fails on main: traversal and zip bomb entries pass unpack.
- [ ] 1.4 Add failing test for missing manifest with and without stated protocols plus malformed manifest. Check: TestPublish_refusesStatedProtocolsWithoutManifest fails on main: stated protocols store without manifest.
- [ ] 1.5 Add failing test for registry slug of each namespace and for rename stability. Check: TestRegistry_servesNamespaceUnderSlug fails on main: unsafe name breaks the registry URL.
- [ ] 1.6 Add failing hostile test for handler 403 then 415 then 413 order without unpack. Check: TestPublish_handlerGuards fails on main: oversize and wrong type reach unpack.
- [ ] 1.7 Add failing hostile test for service exact-set protocol match and fail-closed missing manifest. Check: TestPublish_serviceProtocolHostile fails on main: false protocols store.

## 2. Build

- [ ] 2.1 Build bounded unpack, H1 computation, and manifest protocol check. Check: `go test -race ./pkg/registry/... ./internal/registry/...` passes with new unpack limits, path checks, and manifest parse.
- [ ] 2.2 Wire H1 and slug through store encode, SameContent, missing-H1 backfill, and publish path. Check: `go test -race ./pkg/store/...` passes; republish with changed H1 returns 409 registry.content-changed inside one transaction.
- [ ] 2.3 Wire publish route with unit:manage action on the namespace object and e2e step against the real CLI. Check: `make e2e` registry step passes with tofu init against a namespace outside the safe subset, including mismatch and bad-archive cases.

## 3. Definition of done

- [ ] 3.1 Write docs page for publish with h1, slug, and protocol errors. Check: Docs page commands run as written for publish, slug lookup, and protocol error.
- [ ] 3.2 Run usability check of the full publish and install path. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command.
- [ ] 3.3 Pass definition of done: wiring, route with unit:manage on namespace, e2e step, docs page, usability check, make check. Check: `make check` reports zero findings.
