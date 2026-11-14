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
| `GET /namespaces/-/units/{unit_id}` | `unit:read` on the unit, and `namespace:read` on its namespace | One unit |
| `GET /namespaces/{namespace}/deleted-units` | `namespace:read`, then `unit:read` on each unit | One page of the deleted units |
| `GET /namespaces/{namespace}/collections` | `collection:read` | The collections of the namespace |
| `POST /namespaces/{namespace}/collections` | `collection:create` | A new collection |
| `GET /namespaces/{namespace}/collections/{collection}` | `collection:read` | One collection |
| `DELETE /namespaces/{namespace}/collections/{collection}` | `collection:delete` | No body |
| `PUT /namespaces/{namespace}/collections/{collection}/lock` | `unit:lock` | The manual lock of the collection |
| `GET /namespaces/{namespace}/collections/{collection}/lock` | `collection:read` | The manual lock of the collection |
| `DELETE /namespaces/{namespace}/collections/{collection}/lock` | `unit:lock`, and the caller holds the lock | No body |
| `POST /namespaces/{namespace}/collections/{collection}/lock/force-unlock` | `unit:manage` | No body |
| `PUT /namespaces/{namespace}/lock` | `unit:lock` | The manual lock of the namespace |
| `GET /namespaces/{namespace}/lock` | `namespace:read` | The manual lock of the namespace |
| `DELETE /namespaces/{namespace}/lock` | `unit:lock`, and the caller holds the lock | No body |
| `POST /namespaces/{namespace}/lock/force-unlock` | `unit:manage` | No body |
| `GET /namespaces/{namespace}/output-sharing` | `namespace:read` | The output sharing setting of the namespace |
| `PUT /namespaces/{namespace}/output-sharing` | `unit:manage-sharing` | The new output sharing setting |
| `GET /namespaces/{namespace}/units/{unit_id}/outputs` | `unit:read-outputs` | The root outputs of the latest state of the unit |
| `POST /namespaces/{namespace}/views` | `view:create`, and the caller is a person | A new saved view |
| `GET /namespaces/{namespace}/views` | `view:read`, and the caller is a person | The saved views of the caller |
| `GET /namespaces/{namespace}/views/{view_id}` | `view:read`, and the caller is a person | One saved view of the caller |
| `DELETE /namespaces/{namespace}/views/{view_id}` | `view:delete`, and the caller is a person | No body |
| `GET /namespaces/{namespace}/views/{view_id}/units` | `view:read` and `namespace:read`, then `unit:read` on each unit | One page of the units of the view |
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
- `PUT /api/inventory/unit-lock?unit={unit reference}`, `unit:lock`: take a manual lock. The body is `{"reason": "..."}`.
- `GET /api/inventory/unit-lock?unit={unit reference}`, `unit:read`: read the manual lock of the unit.
- `DELETE /api/inventory/unit-lock?unit={unit reference}`, `unit:lock`: the holder ends the lock.
- `POST /api/inventory/unit-lock/force-unlock?unit={unit reference}`, `unit:manage`: end the lock of another person.

The [manual locks guide](../guides/manual-locks.md) describes the lock routes.
The [output sharing guide](../guides/output-sharing.md) describes the routes of
the output sharing setting and the route that reads the outputs of a unit.

The [saved views guide](../guides/saved-views.md) describes the view routes.

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
| `states` | The states of the unit, as a list of `state` and `since` (an RFC 3339 time in UTC). The list is empty when no state applies. See [Unit states](#unit-states). |
| `resourceCount` | The count of resources. The field is not there when the count is not known. |
| `lock` | `holder`, `since` (an RFC 3339 time in UTC) and `operation`. The field is not there when the unit is not locked. |
| `lastRelease` | The last released lock of an OpenTofu workspace: `id` (the lock ID), `holder`, `releasedBy`, `releasedAt` (an RFC 3339 time in UTC) and `kind` (`normal` or `forced`). A repeated unlock of the same lock succeeds for 24 hours after `releasedAt`, because of this record. The field is not there when the workspace never released a lock, and it is never there for a Pulumi stack. The TFE workspace document does not show it. |
| `manualLock` | The manual lock that covers the unit: `kind` (`unit`, `collection` or `namespace`), `object`, `holder`, `reason` and `since` (an RFC 3339 time in UTC). The field is not there when no manual lock covers the unit. It is separate from `lock`. |
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

## Unit states

A unit can have several states at the same time. The `states` field lists all
of them. No state is the winner. Today the list holds one state: `locked`. The
other states in the table are valid values of the `state` query value, and no
unit has them yet.

| State | Meaning |
| --- | --- |
| `failed` | The latest change to end failed. |
| `awaiting-approval` | A job of the unit waits for an approval. |
| `apply-needed` | A change is ready and nobody applied it. |
| `drifted` | The latest drift check found a difference. |
| `running` | A job of the unit runs. |
| `queued` | A job of the unit waits to start. |
| `locked` | A lock or a manual lock covers the unit. |
| `unknown` | The drift state of the unit is not known. |
| `in-sync` | The latest drift check found no difference. |

The list is in the order of this table. The order is the order of the badges
in the console. It does not rank the states.

`locked` is in the list when the unit has a `lock` field, a `manualLock` field, or both.
This is true for a Pulumi stack with a running update, for an OpenTofu
workspace that holds a lock, and for a unit that a manual lock covers. The
`since` of `locked` is the earlier of the `since` of `lock` and the `since` of
`manualLock`. The server reads both fields from the same entry that it
returns. An unlock, the end of the update, or the end of the manual lock takes
`locked` away. A locked unit keeps its other states.

Send `state` to list only the units that have a state. A unit matches when it
has any of the values. The filter adds to the other filters: a unit must pass
all of them. The match is exact. The server does not change the case, trim
spaces or replace a separator, so `Locked` and `locked ` are not valid. The
server counts the values before it drops duplicates, so 10 values answer 400
also when they hold duplicates. The answer names the valid values.

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/units?state=locked"
```

A unit entry in the answer shows the state like this:

```json
{"states": [{"state": "locked", "since": "2026-10-20T09:30:00Z"}]}
```

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
| `state` | A unit state, for example `locked`. A client can send the name up to 9 times. A unit must have at least one of the states. See [Unit states](#unit-states). |
| `pageSize` | The number of units in a page, from 1 to 500. The default is 100. |
| `pageToken` | The `nextPageToken` of the page before. |
| `at` | A time in Unix milliseconds. The list shows the units at that time. The view at a time has no pages. |

URL-encode the value of a query value: `&`, `#`, `+`, `%` and spaces. The
server splits a `label` value at the first equals sign, so `label=team=payments`
works. A label value cannot hold an equals sign.

A label key has 1 to 128 characters and a label value has 0 to 256
characters. Both use only letters, digits and `. _ : / -`, with no space. A
`label` value outside this form fails with 400 and the code
`request.invalid-query`. A label outside this form, for example a Pulumi stack
tag with a space, shows in the list, but a filter cannot select it.

With more than one `label`, the server reads the units that have the first
label, and checks the other labels on each of them. A query whose labels have
no unit in common answers an empty page, but it can take as long as a read of
every unit with the first label. Put the label with the fewest units first. A
`state` filter has no index of its own: the server reads the units in the
order of the unit ID and checks the lock state of each, so a page can read
every unit of the namespace when the locked units are fewer than a page.

The server answers 400 for a `label`, a `reference` or a `state` value that is
not valid, for more values than the limit, for a `reference` in another namespace, for a
`pageSize` outside the limits, for a `pageToken` that is not valid, and for a
filter or a `pageToken` together with `at`. The list of a `reference` filter
holds only the units that the caller can read. A reference of a unit that does
not exist and a reference of a unit that the caller cannot read give the same
answer: the unit is not in the list.

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/units?label=app=web&label=team=payments&pageSize=50"
```

## Error codes

Each error answer has the field `errorCode` and the header
`Cache-Control: no-store`. The codes are in
[Error codes of the tool-neutral APIs](tool-neutral-error-codes.md).
