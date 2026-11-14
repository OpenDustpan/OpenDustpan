## Context

Tool-neutral list routes use two paging name sets. The registry provider list pages in memory.

## Decisions

1. **One paging name set with store-side provider paging.** Accept pageSize and pageToken on each in-scope /api/dustpan/v1 list route that today accepts limit and cursor, answer nextPageToken alongside nextCursor with the same value, declare paging query parameters in apidesc, page provider addresses in the store with after plus limit, encode the provider page token with internal/pagetoken with its size limit and strict decode, refuse an invalid namespace name in Service.Providers before the store call, and refuse a forged provider token with registry.invalid.

## Structure

Unexported type pageQuery with constructor parsePageQuery, which accepts the new names and the deprecated names, pageSize wins over limit and pageToken wins over cursor, and rejects an invalid winning value with the route error code.

## Risks

- Old clients keep sending limit and cursor, so both sets must stay working.
- SQLite and Postgres collation can differ, so order is fixed to byte-wise address order.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/pagetoken]
```

New imports of this change (each one allowed by the module rules):

- `internal/api/registry` imports `internal/registry/service`
- `internal/registry/service` imports `internal/pagetoken`
- `internal/registry/service` imports `pkg/registry`

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| read amplification | at most limit plus 1 rows per page | rows | GET /api/dustpan/v1/namespaces/{}/registry/providers |

## Contracts

**registry**

- Invariant: Provider addresses order by byte-wise address order within one namespace on each store dialect.
- `Addresses`: pre: Namespace name is valid and kind is provider.. Post: Returns at most limit rows after the after address plus one row to compute the next page token..
- `Providers`: pre: Namespace name passes namespace.ParseName before the store call.. Post: Returns the page of provider addresses and the next page token encoded with internal/pagetoken..

**pagetoken**

- Invariant: Provider page tokens use the shared internal/pagetoken codec with its size limit.
- `Encode`: pre: After address is a valid provider address of the namespace.. Post: Returns an opaque token for the after address..
