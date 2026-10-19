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
| `GET /namespaces/{namespace}/units` | `namespace:read`, then `unit:read` on each unit | The units that the caller can read |
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

## Older paths

The older paths in this table still work. They serve the same handlers with the
same actions, and a released console still calls them. We deprecate them. Use
the new path in new code.

| Older path | Path to use |
| --- | --- |
| `GET /api/inventory/units?organization={namespace}` | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| `GET /api/inventory/deleted-units?organization={namespace}` | `GET /api/dustpan/v1/namespaces/{namespace}/deleted-units` |
| `/api/inventory/namespaces/{namespace}/collections[/{collection}]` | `/api/dustpan/v1/namespaces/{namespace}/collections[/{collection}]` |
| `/api/context/namespaces/{namespace}` | `/api/dustpan/v1/namespaces/{namespace}/context` |
| `/api/context/namespaces/{namespace}/units/{tool}/{unit_id}` | `/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context` |

These routes have no path under `/api/dustpan/v1` yet. Use the older path:

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
| `lastChange` | The time of the last change, in Unix seconds |
| `status` | The status. The field is not there when the status is not known. |
| `resourceCount` | The count of resources. The field is not there when the count is not known. |
| `lock` | `holder`, `since` and `operation`. The field is not there when the unit is not locked. |
| `collection` | The name of the collection. The field is not there when the unit has none. |
| `labels` | The labels of the unit, as an object of strings. The field is not there when the unit has no label. |
| `consoleUrl` | The URL of the page of the unit in the console. The field is not there when the server has no public URL. |

A Pulumi stack has its tags as labels. An OpenTofu workspace has no label yet.

The `consoleUrl` is `{public URL}/units/{unit ID}`. The public URL is the value
of `--public-url`. The path holds the unit ID, so a link stays valid after a
rename.

### Query values

| Name | Content |
| --- | --- |
| `label` | `key=value`. The unit must have the label. A client can send the name up to 10 times. A unit must have each label. |
| `reference` | A unit reference in the namespace of the path. A client can send the name up to 20 times. A unit must have one of the references. |
| `at` | A time in Unix milliseconds. The list shows the units at that time. |

URL-encode the equals sign in a `label` value, for example
`label=team%3Dpayments`. The list splits the value at the first equals sign.

A label key has 1 to 128 characters: letters, digits and `.`, `_`, `:`, `/` and
`-`. A label value has 0 to 256 characters and no control character.

The server answers 400 for a `label` or a `reference` value that is not valid,
for more values than the limit, for a `reference` in another namespace and for
a filter together with `at`. The list of a `reference` filter holds only the
units that the caller can read. A reference of a unit that does not exist and a
reference of a unit that the caller cannot read give the same answer: the unit
is not in the list.

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/units?label=app%3Dweb&label=team%3Dpayments"
```
