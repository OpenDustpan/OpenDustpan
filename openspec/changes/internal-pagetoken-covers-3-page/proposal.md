---
version: 1
---

Issue: #1645.

## Problem

A Developer pages the deleted-units list and other tool-neutral lists and follows nextPageToken. Forged or overlong cursors reach the store range logic instead of failing at the boundary, and a reviewer cannot trust that all page tokens share one strict shape.

## Evidence

- The shared codec package exists with Encode and Decode. Proof: internal/pagetoken/pagetoken.go exists; main lists internal/pagetoken/pagetoken.go:44 (func Encode) and internal/pagetoken/pagetoken.go:58 (func Decode).
- The deleted-units adapter keeps its own encodeCursor and decodeCursor wrappers. Proof: rg encodeCursor first_lines include internal/api/inventory/deleted.go:162:func encodeCursor(c *coreinventory.DeletedCursor, fingerprint string) string; rg decodeCursor first_lines include internal/api/inventory/deleted.go:178:func decodeCursor(s, fingerprint string) (*coreinventory.DeletedCursor, error).
- The deleted wrapper already calls the shared codec on main. Proof: rg pagetoken.Encode first_lines include internal/api/inventory/deleted.go:167; rg pagetoken.Decode first_lines include internal/api/inventory/deleted.go:184.
- The registry provider query keeps its own parseProviderQuery with a pageToken. Proof: rg parseProviderQuery first_lines include internal/api/registry/handlers.go:298:func parseProviderQuery(r *http.Request, ns namespace.Name) (providerQuery, error).
- The registry provider path already calls the shared codec on main. Proof: rg pagetoken.Decode first_lines include internal/api/registry/handlers.go:333 and internal/api/registry/modules.go:177; rg pagetoken.Encode first_lines include internal/api/registry/handlers.go:380 and internal/api/registry/modules.go:225.
- The unit-list paging adapter already calls the shared codec on main. Proof: rg pagetoken.Encode first_lines include internal/api/inventory/paging.go:102; rg pagetoken.Decode first_lines include internal/api/inventory/paging.go:115.
- The shared codec has no strict unknown-field check in the pack evidence. Proof: rg DisallowUnknownFields lists pkg/source/filters.go:109, pkg/backup/restore_operation.go:658, pkg/jobproxy/parse.go:96 with no listed hit in internal/pagetoken/pagetoken.go.

## Fix boundary

- Route inventory deleted-units, registry provider, and namespace page cursors through pagetoken.Encode and pagetoken.Decode with a filter fingerprint and the shared 4 KiB cap.
- In shared Decode reject tokens with unknown JSON members.
- In shared Decode reject tokens with non-whitespace data after the first JSON object.
- Validate decoded Tool, Name, UnitID, and fingerprint at the boundary before the store range read.
- Update the shared codec package doc to state the allowlist rule and the refetch step.

Out of scope:

- A semgrep rule that bans base64 decode of cursor query values outside internal/pagetoken.
- Principal and access cursor consolidation beyond the lists named in the issue.
- New cursor encryption or signing; the fingerprint stays a constant-time compared filter binding.

## Check

- Add failing strict-decode tests for unknown members and trailing data on each wire shape that calls shared Decode. Check: TestPagetokenDecodeRejectsUnknownMember and TestPagetokenDecodeRejectsTrailingData in internal/pagetoken fail on main.
- Add failing boundary tests in NEW internal/api/inventory/deleted_cursor_test.go for deleted-units Tool, Name, and UnitID checks, registry provider fingerprint mismatch, and namespace cursor field checks. Check: TestDeletedCursorRejectsUnknownTool, TestProviderTokenRejectsBadFingerprint, and TestNamespaceCursorRejectsBadField fail on main in NEW internal/api/inventory/deleted_cursor_test.go.
- In internal/pagetoken/pagetoken.go:58 (func Decode) call DisallowUnknownFields, reject non-whitespace trailing data, update the package doc to state that unknown members are rejected, and route deleted, registry provider, and namespace cursors through pagetoken.Encode and pagetoken.Decode with filter fingerprints and field checks. Check: `go test ./internal/pagetoken/... ./internal/api/inventory/... ./internal/api/registry/...` passes.
- Wire check: exercise GET /api/dustpan/v1/namespaces/{}/deleted-units with valid, overlong, fingerprint-mismatch, extra-member, and bad-field tokens through the existing route and gate with an access token. Check: `GET /api/dustpan/v1/namespaces/{}/deleted-units` with a forged token returns an invalid-token error with an access token through the existing namespace grant gate, and the same strict check applies to GET units, registry, and profile list routes; confirmed by CI server tests and e2e list paging.
- Run the full check and the usability walk of the list path from the docs page alone. A person's step. Check: `make check` passes with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The shared Encode function builds an opaque token from an adapter wire struct. (`internal/pagetoken/pagetoken.go:44 (func Encode)`)
- The shared Decode function reads a token into an adapter wire struct. (`internal/pagetoken/pagetoken.go:58 (func Decode)`)
- The deleted-units wrapper decodes its cursor and the provider query reader reads pageSize and pageToken. (`internal/api/registry/handlers.go:298 (func parseProviderQuery)`)
- The store can read one deleted unit by ID. (`pkg/store/postgres/inventory.go:405 (func Store.GetDeletedUnit)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR constrain the page token codec or cursor shape? No. The pack ADR list is empty, so no accepted decision constrains the page-token codec. The choice stays inside this change.
- What happens to old or forged tokens after strict decode? They fail closed with an invalid-token error. The client lists again from the first page with no pageToken on the same route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
