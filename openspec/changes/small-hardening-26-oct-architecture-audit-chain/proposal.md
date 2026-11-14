---
version: 1
---

Issue: #2180.

## Problem

Namespace administrator sets sourcefilter and outputshare and reviews audit history. Unbounded old values bloat audit rows and unaudited writes leave no record.

## Evidence

- Main has an invalid filter test in the source suite. Proof: pkg/source/sourcetest/sourcetest.go:268 (func testInvalidFilter)
- Main has blob delete stack sweep coverage. Proof: pkg/store/storetest/blob_delete.go:499 (func testBlobDeleteStackSweep)
- Main has sweep purge retention checks for stacks and workspaces. Proof: pkg/store/storetest/lifecycle.go:931 (func testSweepPurgeChecksTheRetention)
- Main has namespace settings routes for the settings read and write path. Proof: route table lists ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings

## Fix boundary

- Store SHA-256 and length always for an invalid old filter value in Change.Old, and first 256 bytes only when value is 256 bytes or less.
- Add one audited namespace-setting write helper per dialect for sourcefilter and outputshare.
- Verify final purge frees blobs skipped by the stack sweep for a soft-deleted stack.

Out of scope:

- Route guard address check move into route.Enforce.
- Error-variant name generation.

## Check

- Add failing test for bounded Change.Old on invalid old filter value in pkg/source/sourcetest/sourcetest.go:268. Check: go test ./pkg/source/ -run TestInvalidFilterBoundedOld
- Add failing tests for the NEW audited sourcefilter and outputshare helpers per dialect in NEW pkg/store/storetest/audited_settings_test.go. Check: go test ./pkg/store/ -run TestAuditedNamespaceSettingWrite
- Add failing test that final purge frees blobs skipped by the stack sweep for a soft-deleted stack in pkg/store/storetest/blob_delete.go:499. Check: go test ./pkg/store/ -run TestFinalPurgeFreesSkippedStackBlobs
- Implement bounded Change.Old through Record filter change in pkg/source/sourcetest/sourcetest.go:268 and the shared bound constructor in pkg/audit/audit.go:69. Check: go test ./pkg/source/...
- Implement the NEW audited helpers through Write audited namespace setting in pkg/store/postgres/inventory.go:502 and fix final purge to free skipped blobs in pkg/store/storetest/lifecycle.go:931. Check: go test -race ./pkg/store/... -timeout 60m
- Wire helpers to the existing PATCH /api/dustpan/v1/namespaces/{namespace}/settings route with existing authorization actions and access token in pkg/dustpan/dustpan.go:1369. Check: PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token returns 200.
- Add e2e step for audited namespace setting write against PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token in e2e suite. Confirmed by: CI (e2e job). Check: PATCH /api/dustpan/v1/namespaces/{namespace}/settings with access token writes sourcefilter and audit entry.
- Update docs page for bounded audit old values in docs guides in the same pull request. Check: Docs page for namespace settings lists the 256-byte bound.

Depends on:

- `small-hardening-26-oct-architecture-route-guard-enforcement` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has route.Enforce guard. (`internal/api/route/enforce.go:102 (func Enforce)`)
- Main has invalid filter test in source suite. (`pkg/source/sourcetest/sourcetest.go:268 (func testInvalidFilter)`)
- Main has blob delete stack sweep test. (`pkg/store/storetest/blob_delete.go:499 (func testBlobDeleteStackSweep)`)
- Main has sweep purge retention checks for stacks and workspaces. (`pkg/store/storetest/lifecycle.go:931 (func testSweepPurgeChecksTheRetention)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- What bound applies to Change.Old for an invalid old filter value? No ADR in the pack touches audit Change.Old bounds or namespace-setting helpers. The bound comes from the roadmap scope of this split part.
- Does this change move the address check into route.Enforce? No. The roadmap scope keeps route guard work in the other parts. This part covers only bounded Change.Old, audited helpers, and the purge check.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
