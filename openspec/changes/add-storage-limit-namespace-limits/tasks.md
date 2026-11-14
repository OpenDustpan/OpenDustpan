## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing unit tests in NEW pkg/namespace/storage_limit_test.go for CheckStorageLimit set-limit, refuse Pulumi and OpenTofu writes, exact boundary, lower-positive-wins, and exempt held lease. Check: `go test ./pkg/namespace/... -run TestStorageLimitRefusal -count=1`
- [ ] 1.2 Add server test in NEW internal/servertest/namespace_storage_limit_test.go for PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token, use read, near-limit record, and per-minute audit. Check: `go test ./internal/servertest/... -run TestNamespaceStorageLimit -count=1`

## 2. Definition of done

- [ ] 2.1 Wire installation and namespace limit settings, fenced CheckStorageLimit write-path check with tool error shapes, counter plus background recompute, and use read. Check: PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token returns effective limit and use
- [ ] 2.2 Add e2e step against the real CLIs. Check: e2e step via ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings with an access token then real Pulumi CLI and OpenTofu writes above the limit fail with a shown message naming storage limit with numbers while deletes still work
- [ ] 2.3 Add docs page for the storage limit, numbers in errors, and deletes-reads behavior. Check: `make docs-lint` passes with new storage-limit page
- [ ] 2.4 Run usability check from the docs page, metric check, and full checks. Check: Refusal metric has no namespace label and `make check` with zero findings
