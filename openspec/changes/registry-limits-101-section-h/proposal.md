---
version: 1
---

Issue: #1289, #1292, #2174.

## Problem

An Operator sets safe defaults for a shared store. Without caps a runaway CI loop can send unlimited uploads and versions. The Operator needs predictable guards, clear refusals, and one settings place to raise them.

## Evidence

- Registry upload and version routes exist on main Proof: POST /api/dustpan/v1/namespaces/{}/registry/uploads and GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions
- Registry error codes registry.too-large and registry.invalid exist on main Proof: internal/errcode/catalog_registry.go:29 registry.too-large and internal/errcode/catalog_registry.go:9 registry.invalid
- First slice does not enforce the section H limits Proof: Issue 1289 body: RFC 101 section H proposes limits that the first slice does not enforce
- Action constants for issue 2174 live outside this scope Proof: pkg/identity/identity.go:451 ActionUnitRunApply; issue 2174 body says POST /api/server/roles refuses them

## Fix boundary

- Enforce 60 registry uploads for each principal per fixed UTC hour across all namespaces with the NEW error code registry.upload-rate-limited and Retry-After
- Enforce 1,000 stored versions of one registry address with the NEW error code registry.version-limit under SELECT FOR UPDATE
- Enforce 512 MiB staged-file cap by streaming byte count and 2 GiB release cap at publish with error code registry.too-large and partial blob cleanup
- Add installation settings keys registry.uploadsPerHour, registry.versionsPerAddress, registry.maxFileBytes, and registry.maxReleaseBytes with defaults, bounds, per-request read, effective values, and audit

Out of scope:

- Address-level registry:read and registry:publish objects and publisher and reader roles in issue 1292
- Default registry:read grant for job tokens in issue 1292
- Grantable unit:run-apply, job:read-plans and job:read-logs families in issue 2174

## Check

- Add failing service tests for the hourly cap, version cap, file cap, release cap and settings override Check: TestRegistryUploadRateLimitRefuses61st in pkg/registry fails on main
- Add failing tests that refused uploads do not count, yanked versions count, blob failure releases the count, and streaming abort deletes partial blobs Check: TestRegistryLimitCountingScope in pkg/registry fails on main
- Enforce caps in NEW file pkg/registry/limits.go Check: `go test ./pkg/registry/...` passes
- Add NEW codes registry.upload-rate-limited and registry.version-limit to internal/errcode/catalog_registry.go Check: `go test ./internal/errcode/...` passes
- Edit the baseline migration in place for the hourly counter table, in the NEW path pkg/store/sqlite/migrations/ baseline file and the same under pkg/store/postgres/migrations/, with no numbered migration Check: `go test ./pkg/store/...` passes
- Document the four caps, keys, bounds, Retry-After and codes in docs/reference/registry-api.md Check: `grep -E 'registry.upload-rate-limited|registry.version-limit' docs/reference/registry-api.md` finds the table
- E2e upload burst against the real API with an access token Check: POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token holding registry:publish shows 429 with Retry-After on the 61st upload
- Settings wiring through the installation settings resource Check: PATCH /api/dustpan/v1/settings with an access token updates registry.uploadsPerHour and GET /api/dustpan/v1/settings shows it

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code registry.too-large exists for oversize registry writes (`internal/errcode/catalog_registry.go:29 (func none, code table)`)
- Error code registry.invalid exists for bad registry writes (`internal/errcode/catalog_registry.go:9 (func none, code table)`)
- Registry conformance helper publishes test addresses (`pkg/registry/registrytest/registrytest.go:634 (func publishAddresses)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does RFC 101 or any ADR decide the limits, address objects, or grantable actions? No. The pack holds no ADR text. RFC 101 is discussing, not a decision. This change enforces only the four guards of issue 1289.
- What counts toward the 60 and the 1,000? Only accepted uploads count, per principal subject stable ID across all namespaces, in one fixed UTC hour from database time. Version count includes yanked versions.
- Which error code maps to each limit? Reuse registry.too-large and registry.invalid. Add NEW registry.upload-rate-limited and NEW registry.version-limit.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
