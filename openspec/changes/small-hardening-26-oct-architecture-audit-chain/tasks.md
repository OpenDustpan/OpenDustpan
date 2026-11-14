## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for bounded Change.Old on invalid old filter value in pkg/source/sourcetest/sourcetest.go:268. Check: go test ./pkg/source/ -run TestInvalidFilterBoundedOld
- [ ] 1.2 Add failing tests for the NEW audited sourcefilter and outputshare helpers per dialect in NEW pkg/store/storetest/audited_settings_test.go. Check: go test ./pkg/store/ -run TestAuditedNamespaceSettingWrite
- [ ] 1.3 Add failing test that final purge frees blobs skipped by the stack sweep for a soft-deleted stack in pkg/store/storetest/blob_delete.go:499. Check: go test ./pkg/store/ -run TestFinalPurgeFreesSkippedStackBlobs

## 2. Fix

- [ ] 2.1 Implement bounded Change.Old through Record filter change in pkg/source/sourcetest/sourcetest.go:268 and the shared bound constructor in pkg/audit/audit.go:69. Check: go test ./pkg/source/...
- [ ] 2.2 Implement the NEW audited helpers through Write audited namespace setting in pkg/store/postgres/inventory.go:502 and fix final purge to free skipped blobs in pkg/store/storetest/lifecycle.go:931. Check: go test -race ./pkg/store/... -timeout 60m

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire helpers to the existing PATCH /api/dustpan/v1/namespaces/{namespace}/settings route with existing authorization actions and access token in pkg/dustpan/dustpan.go:1369. Check: PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token returns 200.
- [ ] 4.2 Add e2e step for audited namespace setting write against PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token in e2e suite. Confirmed by: CI (e2e job). Check: PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token writes sourcefilter and audit entry.
- [ ] 4.3 Update docs page for bounded audit old values in docs guides in the same pull request. Check: Docs page for namespace settings lists the 256-byte bound.
- [ ] 4.4 Run usability check of the user path in docs guides. A person's step. Check: Namespace administrator completes setting write from docs page alone.
- [ ] 4.5 Run full check with `make check` in repo root. Confirmed by: CI (`make check`). Check: `make check` with zero findings.
