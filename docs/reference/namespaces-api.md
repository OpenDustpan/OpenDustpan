# Namespaces API

The installation API lists the namespaces that a caller may read. The console
uses this route. A script can use it too.

The path starts with `/api/server`.

## List the namespaces

`GET /api/server/namespaces` answers one page of the namespaces where the
caller holds `namespace:read`, in the order of the name. The route needs only
`caller:read` on the caller.

A grant on the namespace or on the installation puts the namespace in the
list. A grant on a collection, a stack or a workspace does not list its
namespace.

| Query value | Meaning |
| --- | --- |
| `limit` | The largest number of namespaces on the page, from 1 to 1000. The default is 100. |
| `cursor` | The `nextCursor` of the page before, for the same query. |

The answer is one JSON object:

```json
{
  "namespaces": [
    {
      "id": "ns-...",
      "name": "acme",
      "primaryInstallation": "default",
      "failoverVersion": 1,
      "createdAt": "2026-10-18T09:30:00.000Z"
    }
  ],
  "nextCursor": "opaque",
  "total": 1
}
```

| Field | Meaning |
| --- | --- |
| `namespaces` | The page. It is an empty array when the page holds no namespace. |
| `nextCursor` | The cursor of the next page. It is empty on the last page. |
| `total` | The number of namespaces that the query can see. It is on the first page only, and `null` on the pages after it. |

The item fields have these meanings:

| Field | Meaning |
| --- | --- |
| `id` | The stable ID of the namespace. It starts with `ns-`. The ID does not change when the name changes. |
| `name` | The name of the namespace. |
| `primaryInstallation` | The installation that holds the primary copy of the namespace. |
| `failoverVersion` | The version of the failover of the namespace. |
| `createdAt` | The time of the create, in RFC 3339 with nanoseconds. |

## The cursor

The cursor is opaque and sealed. It does not name a namespace. A cursor works
only with the query that made it. The query is the set of namespaces that the
caller can see. The page size is not part of the query, so a caller may change
`limit` between pages.

A cursor that a caller changed, or that another query made, answers 400 with
the error code `request.page-token-invalid`. A cursor of another caller is
another query. Start again without a cursor.

A `limit` that is not a number from 1 to 1000 answers 400 with the error code
`request.page-size-invalid`.
