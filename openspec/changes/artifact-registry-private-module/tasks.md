## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing test for module publish accept and refuse at upload. Check: TestModulePublishAcceptsArchiveSet fails on main, passes after fix.
- [ ] 1.2 Add failing protocol tests for discovery, version list, and download. Check: TestModuleVersionListServesPublishedVersions fails on main, passes after fix.
- [ ] 1.3 Add failing tests for cross-namespace, wrong host, bad name, missing version, duplicate publish, and hidden no-grant answer. Check: TestModuleCrossNamespaceDenied fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Implement module publish checks and module serve routes with namespace grants and audit. Check: `make check` passes with zero findings.
- [ ] 2.2 Wire module serve routes and add e2e step with tofu init against private module. Check: e2e step: `tofu init` with an access token calls GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions on the real binary and fetches the private module of its own namespace.
- [ ] 2.3 Write docs page for private modules. Check: Docs page commands run as written and show publish then tofu init use.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm wiring, route actions, e2e, docs, audit entries, and usability. Check: Route GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions with an access token passes e2e on SQLite and PostgreSQL; routes declare authorization actions; usability check passes; `make check` passes with zero findings.
