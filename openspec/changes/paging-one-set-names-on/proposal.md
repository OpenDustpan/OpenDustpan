---
version: 1
---

Issue: #1390.

## Problem

The tool-neutral APIs use two paging name sets, the registry provider list pages in memory, one provider read skips a namespace name check, paging parameters are not declared in the API description, and one test uses context.Background().

## Evidence

- Registry routes already use the pageSize and pageToken set. Proof: internal/api/registry/operations.go:34:			apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
- The provider read path goes through Service.Providers and store Addresses. Proof: internal/registry/service/read.go:253:	addrs, next, err := s.store.Addresses(ctx, ns, registry.KindProvider, after, limit)
- Principal list routes use the nextCursor name. Proof: internal/api/server/principals.go:134:	NextCursor string              `json:"nextCursor"`
- Namespace list routes use the cursor name. Proof: internal/api/server/namespaces.go:170:var errNamespaceCursor = errors.New("invalid cursor: provide the nextCursor of the page before")

## Fix boundary

- Accept pageSize, pageToken, and nextPageToken on in-scope /api/dustpan/v1 routes that today use limit, cursor, and nextCursor, keep the old names working as deprecated.
- Add store-side after plus limit keyset paging to the registry provider address list.
- Add namespace.ParseName to Service.Providers as a defense-in-depth layer.
- Declare paging query parameters in apidesc on every in-scope list route.
- Change TestProviders to use t.Context().

Out of scope:

- Removal of the deprecated limit, cursor, and nextCursor fields.
- Changes to Pulumi Cloud-compatible paging.
- Changes to TFE-compatible paging.
- Move of console and CLI to the new names.

## Check

- List each /api/dustpan/v1 list route that today accepts limit or cursor, starting from internal/api/server/principals.go and internal/api/server/namespaces.go. Check: `rg -n 'limit|cursor' internal/api/server/principals.go internal/api/server/namespaces.go internal/api/registry/handlers.go` lists each in-scope route before edit.
- In internal/registry/service/read_test.go:270 add failing tests for aliases, conflict precedence, defaults and maximum, dual tokens, forged tokens, authorization and store-side provider paging, using t.Context(). Check: TestProviders alias, conflict, default, forged-token, and paging cases fail on main and pass after the change.
- In internal/registry/service/read.go:239 accept pageSize and pageToken, add namespace.ParseName to Service.Providers with a defense-in-depth comment, and change Addresses in pkg/store/postgres/registry.go:249 and the SQLite adapter to after plus limit keyset paging in byte-wise address order. Check: `go test ./internal/registry/service/... ./pkg/store/...` passes on Postgres and SQLite dialects.
- Declare pageSize and pageToken in internal/api/registry/operations.go:34 and in the apidesc operations of the principal and namespace list routes, with old names marked deprecated. Check: `go test ./internal/registry/service/...` passes and openapi.json lists pageSize and pageToken on each in-scope list route.
- Wire GET /api/dustpan/v1/namespaces/{}/registry/providers with its authorization actions, run the e2e list step with an access token, update the docs page, run a usability check, and run make check with zero findings. Check: GET /api/dustpan/v1/namespaces/{}/registry/providers with an access token pages with pageSize and pageToken; `make check` reports zero findings. Confirmed by: CI (make e2e and make check).

Depends on:

- internal-pagetoken-covers-3-page
- `internal-pagetoken-covers-3-page` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Store already has Addresses for registry addresses. (`pkg/store/postgres/registry.go:249 (func registryStore.Addresses)`)
- Registry port already defines Addresses. (`pkg/registry/ports.go:65`)
- Service already has Providers for the provider list. (`internal/registry/service/read.go:239 (func Service.Providers)`)
- Providers test already exists. (`internal/registry/service/read_test.go:270`)
- namespace.ParseName already exists for defense in depth. (`pkg/namespace/namespace.go:79 (func ParseName)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide paging names? No ADR in the pack touches paging names, so no ADR decision applies. The pack ADR list is empty.
- Should the old paging names be removed? No. Old limit, cursor, and nextCursor fields stay working and are marked deprecated because released clients use them.
- Which name wins when both name sets are sent? pageSize wins over limit and pageToken wins over cursor. An invalid value in the ignored losing set is ignored and is not refused.
- Should the provider page token use internal/pagetoken? Yes. The provider page token uses internal/pagetoken with its size limit and strict decode, after internal-pagetoken-covers-3-page lands, so no new cursor codec is added.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
