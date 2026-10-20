# Save and open a view of the unit list

This guide shows how to save the filters of the unit list under a name, how
to open the saved view again, and how to delete it.

A saved view is a name for a query of the unit list. The server stores the
query and never the units. When you open a view, the server runs the query
again with your access. A view shows only the units that you can read with the
action `unit:read`. A saved view gives no access to any unit.

The paths in this guide are under `/api/dustpan/v1/`. They work with the same
token as the unit list. See `docs/reference/inventory-api.md` for the list of
paths.

## What a view holds

A view has a name and a query. The query has the same two filters as the unit
list:

| Filter | Content | Limit |
| --- | --- | --- |
| `labels` | A list of `key` and `value` pairs. A unit must have each label. | 10 labels |
| `references` | A list of unit references in the namespace of the view, for example `pulumi:acme/web/prod`. A unit must be one of them. | 20 references |

A label key has 1 to 128 characters: letters, digits and the characters
`. _ : / -`. A label value has at most 256 characters and no control
character. A reference holds the tool of the unit. If a unit moves to another
tool, it no longer matches the reference. A label filter still matches it.

A name has 1 to 128 characters. It has no control character, no hidden
character and no space at the start or at the end. The name is unique for
each person in a namespace. The server compares names exactly, so `Payments`
and `payments` are two names.

The server limits the use of views:

| Limit | Value |
| --- | --- |
| Views for each person in a namespace | 100 |
| Views in a namespace, for all people | 10 000 |
| Size of a request body | 16 KiB |

A request that goes over a limit gets an error. The error text names the
limit.

## Who owns a view

One person owns a view in one namespace. Only that person can read, open or
delete it. An administrator cannot read the view of another person.

Only a person makes a view. These callers get the answer 403 with the text
`only a person has saved views: sign in as a person`:

- the bootstrap administrator token
- a service principal and a workload
- a principal of a namespace
- a job or a runner
- a caller whose kind the server does not know

Sign in as a person, for example through the console, and use a personal
access token of that person.

The server stores the ID of your principal as the owner of the view
(`prn-...`), and not your subject. If an administrator deletes your principal
and a new principal gets the same subject, the new principal does not see your
views. The server deletes the views of a deleted principal later. Until it
does, the views stay closed to every reader.

A view is private. No call shares a view with a namespace or with a
collection.

## Make a view

`POST /api/dustpan/v1/namespaces/<namespace>/views` makes a view. It needs the
action `view:create` on the namespace. Each built-in role that holds
`namespace:read` also holds `view:create`, `view:read` and `view:delete`.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"name":"Payments","query":{"labels":[{"key":"team","value":"payments"}],"references":[]}}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/views"
```

The body names the `name` and, if you want a filter, the `query`. The body
must not name an unknown field. The answer is 201 with the view and a
`Location` header:

```json
{
  "id": "view-0123456789abcdef0123456789abcdef",
  "namespace": "acme",
  "name": "Payments",
  "query": {
    "labels": [{"key": "team", "value": "payments"}],
    "references": []
  },
  "createdAt": "2026-10-20T12:00:00Z"
}
```

The answer is 409 if you already have a view with the name, or if you reached a
limit. The answer is 400 if the name or the query is not valid. The server
changes nothing in these cases.

## List and read your views

`GET /api/dustpan/v1/namespaces/<namespace>/views` answers `{"views": [...]}`
with your views in this namespace, in the order of the name. The list holds at
most 100 views, so it has no page token.

`GET /api/dustpan/v1/namespaces/<namespace>/views/<view ID>` answers one view.
Both routes need the action `view:read` on the namespace.

A view of another person answers 404, with the same text as a view that does
not exist.

## Open a view

`GET /api/dustpan/v1/namespaces/<namespace>/views/<view ID>/units` runs the
query of the view and answers one page of the units, in the form of the unit
list. The route needs the actions `view:read` and `namespace:read` on the
namespace. The server checks `unit:read` for each unit again, so the page
holds only the units that you can read.

The page of a view is the page that the unit list gives for the same query.
This route takes the query values `pageSize` and `pageToken` of the unit list.
It does not take `label`, `reference` or `at`, because the view holds the
filters. A request with another query value gets the answer 400.

```sh
curl -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/views/view-0123456789abcdef0123456789abcdef/units?pageSize=50"
```

## Delete a view

`DELETE /api/dustpan/v1/namespaces/<namespace>/views/<view ID>` deletes your
view. It needs the action `view:delete` on the namespace. The answer is 204. A
view that does not exist, or that belongs to another person, answers 404.

## Retry a call safely

The create and the delete accept the `Idempotency-Key` header. Send a new
random key for each call that you want to retry safely.

A view can hold a label value that names something private. For this reason
the server does not keep the view for a replay. It keeps the ID of the view.
If you send the same key and the same body again, the server makes no second
view. It answers 409 with the code `request.idempotency-key-replayed-secret`
and the ID of the view that the first call made. Read the view with that ID.

A delete that you send again with the same key answers 204 and the header
`Idempotent-Replayed: true`.

## Audit log

Each create and each delete adds one entry to the audit log of the namespace.
The entry has the action `view:create` or `view:delete` and the ID of the view.
It never holds the name or the query of the view.

When the server deletes the views of a deleted principal, it adds one
`view:delete` entry for each view. The subject of these entries is `@server`.
They hold the view ID and no name.

## Back up a view

The table of saved views is part of a backup. A restore checks each row again
with the rules of a new view. A row that does not pass makes the restore fail
with a corrupt backup error. The server never serves a row that fails these
rules.
