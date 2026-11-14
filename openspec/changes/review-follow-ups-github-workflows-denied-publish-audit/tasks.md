## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing module refusal test in NEW internal/registry/service/module_test.go for the refusal path in internal/registry/service/module.go Check: NEW TestDeniedModulePublishWritesAuditAndStoresNothing fails on main
- [ ] 1.2 Add failing provider refusal test in NEW internal/registry/service/module_test.go for the same path Check: NEW TestDeniedProviderPublishWritesAuditAndStoresNothing fails on main

## 2. Fix

- [ ] 2.1 Record denied publishes in the namespace audit log without storage in internal/registry/service/module.go Check: The two new tests pass

## 3. Definition of done

- [ ] 3.1 Wire the audit write into the publish refusal path and check the audit read Check: `GET /api/audit/entries` with an access token shows the denied publish entry
- [ ] 3.2 Confirm no release for a denied publish Check: `GET /api/dustpan/v1/namespaces/{namespace}/registry/modules` with an access token lists no denied version
- [ ] 3.3 Run the full checks Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
