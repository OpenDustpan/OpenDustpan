# Deleted units

This guide shows how to list, restore and purge deleted units through the
inventory API. It follows ADR 47.

A delete is a soft delete. `pulumi stack rm` and `tofu workspace delete`
take the unit out of every list and free its name at once. OpenDustpan keeps
the state, the history and the secrets of the unit until its retention
period ends.

The server does not purge a unit on its own yet. The automatic purge at the
end of the retention period comes in a later change (ADR 47). Until then, a
deleted unit stays until an admin purges it through the API, also after its
retention period ends.

## Set the retention period

`--soft-delete-retention` (`DUSTPAN_SOFT_DELETE_RETENTION`) sets the
retention period. The default is `720h` (30 days). The minimum is `1h`. A
new period applies to units that you delete after the change. A unit that is
already deleted keeps its purge time.

## List the deleted units

`GET /api/inventory/deleted-units?organization=<namespace>` lists one page of
the deleted units of the namespace, ordered by tool, then name, then unit ID.
It needs the action `namespace:read` on the namespace. The list holds only
the units that you can read: a grant of `unit:read` on the installation or
on the namespace shows every deleted unit of the namespace. A grant on one
unit shows nothing after the unit is deleted, because a grant on a unit
follows a unit that exists.

These query values filter and page the list:

- `tool`: `pulumi` or `opentofu` (the labels `Pulumi` and `OpenTofu` also
  work).
- `namePrefix`: the start of the unit name, case-sensitive. For a Pulumi
  stack the name is `<project>/<stack>`.
- `purgeWithinDays`: only units whose retention period ends in this number
  of days or less, from 0 to 3650. A unit whose retention period has ended
  also matches.
- `limit`: the page size, from 1 to 500. The default (missing or 0) is 50.
- `cursor`: the `nextCursor` value of the previous page.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/inventory/deleted-units?organization=acme&tool=pulumi&limit=20"
```

The answer has `units`, `total` and `nextCursor`. Each unit has `tool`,
`reference`, `namespace`, `name`, `unitID`, `deletedAt` and `purgeAfter`
(RFC 3339 times in UTC). `total` counts the deleted units that match the
filters and that you can read. `nextCursor` is empty on the last page.

## Restore a deleted unit

`POST /api/inventory/deleted-units/<tool>/<unitID>/restore?organization=<namespace>`
restores the unit with its state, its history and its secrets. It needs the
action `unit:restore`. The maintainer and admin roles have it.

Send `{}` to restore the unit under its name before the delete. If a live
unit holds that name, the server answers 409. Then send a new name:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  -d '{"name": "prod-restored"}' \
  "https://dustpan.example/api/inventory/deleted-units/pulumi/<unitID>/restore?organization=acme"
```

For a Pulumi stack the new name is the stack name. The stack stays in its
project. The server checks the new name with the name rules of the tool and
answers 400 for a name that is not valid. The answer is the live unit with
the same `unitID`.

## Purge a deleted unit

`DELETE /api/inventory/deleted-units/<tool>/<unitID>?organization=<namespace>`
deletes the unit for good before its retention period ends. It needs the
action `unit:purge`. Only the admin role has it. The server answers 204.
A purge cannot be undone.

## Errors

- 404: the namespace has no deleted unit with this tool and ID. A live unit
  or a purged unit also answers 404.
- 409: a live unit holds the name, or a failover fenced the write.
- 421: the namespace is not homed in this region.

A restore and a purge of the same unit can run at the same time. Each runs
in its own fenced transaction. The second one answers 404 or 409 and changes
nothing.

Each restore and each purge writes one audit entry with the kind `change`.
See [Audit log](audit-log.md).
