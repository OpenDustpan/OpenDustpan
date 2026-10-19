# Inventory API

The inventory API lists the units of a namespace for all tools in one
document. It also serves the context of a namespace and of a unit. A client
that reads the units of a namespace, for example a developer portal, uses
only this API.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this
page is relative to that prefix.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /namespaces/{namespace}/units` | `namespace:read`, then `unit:read` on each unit | One page of the units that the caller can read |
| `GET /units/{unit_id}` | `unit:read` on the unit, and `namespace:read` on its namespace | One unit |
| `GET /namespaces/{namespace}/deleted-units` | `namespace:read`, then `unit:read` on each unit | One page of the deleted units |
| `GET /namespaces/{namespace}/collections` | `collection:read` | The collections of the namespace |
| `POST /namespaces/{namespace}/collections` | `collection:create` | A new collection |
| `GET /namespaces/{namespace}/collections/{collection}` | `collection:read` | One collection |
| `DELETE /namespaces/{namespace}/collections/{collection}` | `collection:delete` | No body |
| `GET /namespaces/{namespace}/context` | `namespace:read` | The context of the namespace |
| `PATCH /namespaces/{namespace}/context` | `namespace:set-context` | The new context |
| `DELETE /namespaces/{namespace}/context` | `namespace:set-context` | No body |
| `GET /namespaces/{namespace}/units/{unit_id}/context` | `unit:read` | The context document of the unit |
| `PATCH /namespaces/{namespace}/units/{unit_id}/context` | `unit:set-context` | The new context |
| `DELETE /namespaces/{namespace}/units/{unit_id}/context` | `unit:set-context` | No body |

A unit path holds the unit ID. The unit ID never changes, also when a person
renames the stack or the workspace. The tool is a field of the unit and is not
part of the path.

A caller who cannot read a namespace gets 403 on the unit list. The unit list
holds only the units that the caller can read with `unit:read`. A caller with
`namespace:read` and `unit:read` only gets 403 on each route that changes data.

These routes have no path under `/api/dustpan/v1` yet. Use these paths:

- `POST /api/inventory/deleted-units/{tool}/{unitID}/restore?organization={namespace}`
- `DELETE /api/inventory/deleted-units/{tool}/{unitID}?organization={namespace}`
- `PUT /api/inventory/unit-collection?unit={unit reference}`

## The unit list

`GET /api/dustpan/v1/namespaces/{namespace}/units` answers
`{"units": [...]}`. Each entry has these fields:

| Field | Content |
| --- | --- |
| `tool` | The label of the tool: `Pulumi` or `OpenTofu` |
| `reference` | The unit reference, for example `pulumi:acme/web/prod` |
| `unitID` | The ID of the unit. A rename keeps it. |
| `namespace` | The namespace of the unit |
| `name` | The own name of the unit in its tool |
| `lastChange` | The time of the last change, an RFC 3339 time in UTC |
| `status` | The status. The field is not there when the status is not known. |
| `resourceCount` | The count of resources. The field is not there when the count is not known. |
| `lock` | `holder`, `since` (an RFC 3339 time in UTC) and `operation`. The field is not there when the unit is not locked. |
| `collection` | The name of the collection. The field is not there when the unit has none. |
| `labels` | The labels of the unit, as an object of strings. The field is not there when the unit has no label. |
| `consoleUrl` | The URL of the page of the unit in the console. The field is not there when the server has no public URL, and not for a unit that is deleted. |

A Pulumi stack has its tags as labels. The server writes the labels to the
inventory in the same transaction as the tag change. An OpenTofu workspace has
no label yet.

The `consoleUrl` is `{public URL}/units/{unit ID}`. The public URL is the value
of `--public-url`. The path holds the unit ID, so a link stays valid after a
rename.

The list is in the order of the unit ID. It answers one page. The answer has
`nextPageToken` when a later page exists, and has no such field on the last
page. Send the value as `pageToken` to read the next page, with the same
namespace and the same filters. A `pageToken` from another query answers 400.

## One unit by its ID

`GET /api/dustpan/v1/namespaces/-/units/{unit_id}` answers one unit entry with
the same fields as an item of the unit list. The path puts `-` in the place of
the namespace, because the unit ID is unique in the installation. Use it when a
client holds only a unit ID, for example from a `consoleUrl`.

The server answers 404 with `{"code": 404, "message": "unit not found"}` in
these cases:

- The unit ID is not known.
- The unit is deleted. Read a deleted unit from the list of deleted units.
- The caller has no `unit:read` on the unit.
- The caller has no `namespace:read` on the namespace of the unit.

The four answers have the same body. A caller cannot learn from the answer
whether a unit exists. The server answers 401 when the request has no valid
token.

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/-/units/$UNIT_ID"
```

### Query values

| Name | Content |
| --- | --- |
| `label` | `key=value`. The unit must have the label. A client can send the name up to 10 times. A unit must have each label. |
| `reference` | A unit reference in the namespace of the path. A client can send the name up to 20 times. A unit must have one of the references. |
| `pageSize` | The number of units in a page, from 1 to 500. The default is 100. |
| `pageToken` | The `nextPageToken` of the page before. |
| `at` | A time in Unix milliseconds. The list shows the units at that time. The view at a time has no pages. |

URL-encode the value of a query value: `&`, `#`, `+`, `%` and spaces. The
server splits a `label` value at the first equals sign, so `label=team=payments`
works, and a value can hold an equals sign.

A label key and a label value follow the grammar in `GLOSSARY.md` under
"Label". A label outside this grammar shows in the list, but a filter cannot
select it.

The server answers 400 for a `label` or a `reference` value that is not valid,
for more values than the limit, for a `reference` in another namespace, for a
`pageSize` outside the limits, for a `pageToken` that is not valid, and for a
filter or a `pageToken` together with `at`. The list of a `reference` filter
holds only the units that the caller can read. A reference of a unit that does
not exist and a reference of a unit that the caller cannot read give the same
answer: the unit is not in the list.

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/units?label=app=web&label=team=payments&pageSize=50"
```
