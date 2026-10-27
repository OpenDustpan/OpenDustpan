# Deleted units

This guide shows how to list, restore and purge deleted units through the
inventory API.

A delete is a soft delete. `pulumi stack rm` and `tofu workspace delete`
take the unit out of every list and free its name at once. OpenDustpan keeps
the state, the history and the secrets of the unit until its retention
period ends.

When the retention period of a unit ends, the retention expiry purges the
unit. An admin can also purge a unit through the API before that time.

The list of deleted units has a path under `/api/dustpan/v1`. The restore and
the final delete have no path under `/api/dustpan/v1` yet: they use the paths
under `/api/inventory/` that this guide shows. See
`docs/reference/inventory-api.md`.

## Set the retention period

`--soft-delete-retention` (`DUSTPAN_SOFT_DELETE_RETENTION`) sets the
retention period. The default is `720h` (30 days). The minimum is `1h`. A
new period applies to units that you delete after the change. A unit that is
already deleted keeps its purge time.

## List the deleted units

`GET /api/dustpan/v1/namespaces/<namespace>/deleted-units` lists one page of
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
- `pageSize`: the page size, from 1 to 500. The default (missing) is 50.
- `pageToken`: the `nextPageToken` value of the previous page. Send it with the same `tool`, `namePrefix` and `purgeWithinDays` values. A `pageToken` from a list with other filters answers 400. The `pageSize` can change from page to page.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/deleted-units?tool=pulumi&pageSize=20"
```

The answer has `units`, `total` and `nextPageToken`. Each unit has `tool`,
`reference`, `namespace`, `name`, `unitID`, `deletedAt` and `purgeAfter`
(RFC 3339 times in UTC). `total` counts the deleted units that match the
filters and that you can read. `nextPageToken` is not in the answer on the last page.

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
- 409: the new name holds the gates of a deleted unit. Purge or restore that
  unit first.
- 409: a live unit holds the name of the deleted unit and shares its gates.
  Delete the live unit, then restore the deleted unit under its name.
- 403: a gate or an access limit refuses the restore or the purge. The message
  holds the message of the gate, the error code and the ID of the decision record.
  See [Gates and access limits on state writes](state-write-gates.md).
- 409: a gate gave an error. The message holds the error code and the ID of the
  decision record.
- 503: the policy engine did not answer.
- 421: this installation is not the primary installation of the namespace.

A restore and a purge of the same unit can run at the same time. Each runs
in its own fenced transaction. The second one answers 404 or 409 and changes
nothing.

Each restore and each purge writes one audit entry with the kind `change`.
See [Audit log](audit-log.md).

## The retention expiry

The server runs the retention expiry one time each hour. The first run starts
one hour after the server starts. The run does two things:

1. It purges each deleted unit whose retention period ended, in every
   namespace whose primary installation is this installation. Each purge runs in its own
   fenced transaction and writes one audit entry with the subject `@server`
   and the action `unit:purge`.
2. It finishes the blob purge records. A purge deletes the blobs of the unit
   after its rows, and keeps a blob purge record. A checkpoint save or a
   state upload that started before the purge can still write a blob after
   that delete. So the run deletes the blobs of a record again when the
   record is older than 24 hours, and finishes the record only when no blob
   remains.

An expiry run purges a unit only when its retention period ended at the start of
the run. The purge checks this again in its transaction. So a unit that you
restore and delete again during an expiry run gets its new retention period. A
restore and an expiry purge of the same unit can run at the same time. One of
them wins, and the run counts the other unit as skipped. A failed purge
does not stop the run. The server logs a warning with the unit reference
and the error kind, and the next run tries again.

When an expiry run purges units, the server logs one line with the count and the
oldest purge time among them.

`POST /api/server/deleted-units/sweep` runs the retention expiry now. It needs the action
`server:maintenance` on the installation. Each purge writes its audit entry
with you as the subject. While another expiry run continues on the server, the route
answers 409 at once. Try again later. The answer names each purged unit and
counts the other outcomes:

```json
{
  "purged": [{"tool": "pulumi", "namespace": "acme", "unitID": "stk-1", "reference": "pulumi:acme/web/prod"}],
  "failed": 0,
  "skipped": 0,
  "finishedBlobPurges": 1,
  "pendingBlobPurges": 2
}
```

The metric `dustpan_deleted_unit_sweep_total` counts the outcomes of each
expiry run. See [Observe](observe.md).

## After an upgrade or an installation restore

At the start, the server logs one line with the count of deleted units whose
retention period has already ended. The first run, one hour later, purges
them. After an upgrade, these are units that you deleted before the server
had an expiry run. After an installation restore, these are units whose retention
period ended after the backup.

To keep such a unit, restore it before the first run. First list the
deleted units whose retention period ends within one day. The units that the
first run purges are in this list. The list holds the units of one
namespace, so list each namespace. The list comes in pages: when the answer has a
`nextPageToken`, send it as `pageToken` to get the next page.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/deleted-units?purgeWithinDays=1"
```

Then restore each unit that you keep, with the `tool` and the `unitID` of the
list:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" -d '{}' \
  "https://dustpan.example/api/inventory/deleted-units/<tool>/<unitID>/restore?organization=acme"
```

If a live unit holds its name, the server answers 409. Then send a new name,
as [Restore a deleted unit](#restore-a-deleted-unit) shows.

The retention setting
does not help here: a delete records the end of the retention period of the
unit, and a new setting applies only to later deletes. The retention setting
has no value that turns the purge off.
