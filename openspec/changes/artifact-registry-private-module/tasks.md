## 1. Failing tests

- [ ] 1.1 Add failing test for module publish accept and refuse at upload. Check: TestModulePublishAcceptsArchiveSet fails on main, passes after fix.
- [ ] 1.2 Add failing protocol tests for discovery, version list, and download. Check: TestModuleVersionListServesPublishedVersions fails on main, passes after fix.
- [ ] 1.3 Add failing tests for cross-namespace, wrong host, bad name, missing version, and forbidden caller. Check: TestModuleCrossNamespaceDenied fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Implement module publish checks and serve routes with namespace grants and audit. Check: `make check` passes with zero findings.
- [ ] 2.2 Wire routes and add e2e step with tofu init against private module. Check: `tofu init` against the real binary over TLS fetches a private module from its own namespace registry.
- [ ] 2.3 Write docs page for private modules. Check: Docs page commands run as written and show publish then tofu init use.

## 3. Definition of done

- [ ] 3.1 Confirm wiring, route actions, e2e, docs, audit entries, and usability check. Check: Routes declare authorization actions; e2e step passes on SQLite and PostgreSQL; usability check passes; `make check` passes with zero findings.
