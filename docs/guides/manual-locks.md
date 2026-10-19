# Manual locks

This guide shows how to put a manual lock on one unit or on one collection, what
the lock stops now, and how to remove the lock.

A manual lock is a lock that a person puts on an object, with a reason that
everyone sees. It is not the lock of a tool. OpenTofu takes its own lock on a
workspace, and the Pulumi CLI takes an update lease on a stack. A tool takes and
releases its own lock. A person takes and removes a manual lock through the
API, and a force-unlock of a tool never removes it.

## What a manual lock stops

This version of manual locks stops two things. While a manual lock covers a unit:

- A lock request of OpenTofu for the workspace fails, on the OpenTofu HTTP
  backend and on the cloud block. `tofu plan` and `tofu apply` with the remote
  lock stop before they read or write state.
- The start of a mutating update of the Pulumi CLI for the stack fails. `pulumi
  up`, `pulumi refresh` and `pulumi destroy` stop. `pulumi preview` still works.

A job phase that started before the lock runs on. When that phase asks for a
lock of OpenTofu or for the start of a mutating Pulumi update, the request
meets the lock and fails. A request that is in progress at the moment of the
lock can pass: the check and the request run in separate transactions. The next
request meets the lock.

A tool that already holds its own lock on the unit keeps it. The lock holder can
still write state and release the lock.

## What a manual lock does not stop yet

A manual lock does not stop these changes. Do not use a manual lock as the only
protection against them:

- A write of state through the OpenTofu HTTP backend that names no lock. The
  write of a client that holds no lock is not checked against the manual lock.
- `pulumi stack import`.
- A removal or an edit of the state of a unit, for example `pulumi state delete`.
- A removal of the unit.

A manual lock does not stop reads. A tool force-unlock, for example
`tofu force-unlock`, ends the lock of the tool only.

## What the tools print

The text names the unit in the words of the tool, the holder, the time and the
reason. The Pulumi CLI prints, for a stack in a locked collection:

```text
stack acme/web/prod is in collection acme/payments, which has a manual lock by alice since 2026-10-19T10:00:00Z: change window closed
```

For a lock on the stack itself the text starts with `stack acme/web/prod has a
manual lock by`. OpenTofu names a `workspace` in the same way.

The OpenTofu HTTP backend shows the lock information of the answer 423. This is
the output of `tofu apply` for a lock of `admin` on the workspace
`acme/e2e-workspace` (OpenTofu 1.12.7):

```text
Error: Error acquiring the state lock

Error message: HTTP remote state already locked: ID=A manual lock blocks this
run. To remove it, send `DELETE
/api/inventory/unit-lock?unit=opentofu:acme/e2e-workspace`.
Lock Info:
  ID:        A manual lock blocks this run. To remove it, send `DELETE /api/inventory/unit-lock?unit=opentofu:acme/e2e-workspace`.
  Path:      
  Operation: manual lock
  Who:       admin
  Version:   
  Created:   2026-10-19 21:55:37 +0000 UTC
  Info:      workspace acme/e2e-workspace has a manual lock by admin since 2026-10-19T21:55:37Z: change window closed. `tofu force-unlock` does not remove a manual lock. The holder sends `DELETE /api/inventory/unit-lock?unit=opentofu:acme/e2e-workspace`. A person with unit:manage sends `POST /api/inventory/unit-lock/force-unlock?unit=opentofu:acme/e2e-workspace`.
```

The `ID` is no lock ID of a tool, so `tofu force-unlock` cannot use it. For a
lock on a collection, the routes are the routes of the collection. The cloud
block of OpenTofu prints its own line for a refused lock request, `workspace
already locked`, because it does not show the detail of the server answer. The
answer of the server to the lock request holds the full text, and the inventory
shows the lock on the unit.

## The object of a lock

A manual lock is on one unit or on one collection.

- A lock on a unit covers that unit. A rename of the unit keeps the lock.
- A lock on a collection covers each unit that is in the collection now. A unit
  that joins the collection is covered from that moment. A unit that leaves the
  collection is not covered any more.
- A unit can have its own lock and be in a locked collection. The lock of the
  unit shows first. Removing it shows the lock of the collection.

Deleting an empty collection removes its lock. A collection that someone creates
again under the same name has no lock.

## Roles

| Step | Action | Object | Roles |
| --- | --- | --- | --- |
| Take a lock | `unit:lock` | The unit or the collection | `maintainer`, `admin` |
| Remove your own lock | `unit:lock` | The unit or the collection | `maintainer`, `admin` |
| Remove the lock of another person (force-unlock) | `unit:manage` | The unit or the collection | `maintainer`, `admin` |
| Read a lock | `unit:read` for a unit, `collection:read` for a collection | The unit or the collection | Every role that reads |

`namespace:break-freeze` does not pass a manual lock. The way out is the
force-unlock.

## Take a lock

The body gives the reason. The reason is required and has at most 500
characters. It has no line break and no other control character.

For a unit, `PUT /api/inventory/unit-lock?unit=<unit reference>`:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"reason": "change window closed"}' \
  "https://dustpan.example/api/inventory/unit-lock?unit=pulumi:acme/web/prod"
```

For a collection, `PUT /api/dustpan/v1/namespaces/<namespace>/collections/<collection>/lock`:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"reason": "change window closed"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/collections/payments/lock"
```

The answer is 200 with the lock:

```json
{
  "kind": "collection",
  "object": "acme/payments",
  "holder": "alice",
  "reason": "change window closed",
  "since": "2026-10-19T10:00:00Z"
}
```

`kind` is `unit` or `collection`. `object` is the unit reference or
`<namespace>/<collection>`. `holder` is the subject of the person that took the
lock. `since` is an RFC 3339 time in UTC.

The server answers:

- 400 when the body has no reason, an unknown field, or a reason that is not
  valid.
- 404 when the unit or the collection does not exist.
- 409 when the object already has a manual lock. The message names the holder
  and the reason. Remove the lock, or ask the holder to remove it.

## Read a lock

`GET` on the same path answers the lock, or 404 when the object has no manual
lock. A unit in the unit list has the field `manualLock` when a lock covers it,
for a lock on the unit and for a lock on its collection. The field has the same
shape as the answer above. The field `object` names the object that holds the
lock. The field `lock` of the unit stays the lock of the tool.

## Remove a lock

The holder sends `DELETE` on the same path. The answer is 204. The answer is
409, and the lock stays, in these two cases:

- Another person holds the lock.
- The lock changed while the server removed it. A person ended the lock and took
  a new one in the same moment. The new lock stays. Read the lock again, then
  decide again.

A person with `unit:manage` on the object removes the lock of another person
with a force-unlock. The answer is 204:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/inventory/unit-lock/force-unlock?unit=pulumi:acme/web/prod"

curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/collections/payments/lock/force-unlock"
```

A force-unlock answers 409 when the lock changed while the server removed it.
The new lock stays. Read the lock again, then decide again.

## Audit

Each lock and each removal writes an audit entry in the same transaction as the
change. The entry names the person, the action, the object and the reason. A
removal names the holder, the reason and the time of the lock that it ends.
A force-unlock has the action `unit:manage` and the version `force-unlock`.
