## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests and RFC first

- [ ] 1.1 Add failing store and service tests for Rewrap, RotateNamespaceKey, and old-version reads in pkg/durable/durabletest/durabletest_test.go:35. Check: TestRewrapKeepsOldItemKeysReadable in pkg/durable/durabletest/durabletest_test.go:35 fails on main, passes after.
- [ ] 1.2 Add failing API tests for the NEW route POST re-encrypt as a durable operation with audit entries in pkg/dustpan/context_test.go:746. Check: TestReEncryptRunsAsDurableOperation in pkg/dustpan/context_test.go:746 fails on main, passes after.
- [ ] 1.3 Write the RFC for one wrap for each installation in the new file docs/rfc/0089-namespace-key-per-installation-wraps.md. Check: Add the new file docs/rfc/0089-namespace-key-per-installation-wraps.md with options and owner questions for the NEW RFC.

## 2. Build

- [ ] 2.1 Build Rewrap and RotateNamespaceKey on the namespace key record in pkg/namespace/namespace.go. Check: `GET /api/dustpan/v1/namespaces/{namespace}/key` on the NEW route shows new active version after rotate in pkg/namespace/namespace.go.
- [ ] 2.2 Build StartReEncrypt as a durable operation with fence checks and audit entries in pkg/store/postgres/state_close.go:91. Check: The NEW route POST /api/dustpan/v1/namespaces/{namespace}/key/re-encrypt answers 202 with an operation ID in pkg/dustpan/context_test.go:746.
- [ ] 2.3 Keep old versions while a backup record names them in pkg/store/postgres/durable.go:44. Check: Old item key blob opens after rewrap in sqlite and postgres suites in pkg/store/postgres/durable.go:44.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the NEW routes, actions, audit kinds, and docs page. Check: Route declares namespace:manage-key on the namespace.
- [ ] 4.2 Run usability check and record friction. Check: Persona completes rotate and re-encrypt from the docs page alone.
- [ ] 4.3 Run full check with no findings. Check: `make check` reports zero findings.
