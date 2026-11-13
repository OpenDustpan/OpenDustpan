# Manual locks

This guide shows how to put a manual lock on one stack, one workspace, one
collection or a whole namespace, what the lock stops, and how to remove the lock.

A manual lock is a lock that a person puts on an object, with a reason that
everyone sees. It is not the lock of a tool. OpenTofu takes its own lock on a
workspace, and the Pulumi CLI takes an update lease on a stack. A tool takes and
releases its own lock. A person takes and removes a manual lock through the
API, and a force-unlock of a tool never removes it.

## What a manual lock stops

While a manual lock covers a stack or a workspace, these requests fail:

- A lock request of OpenTofu for the workspace, on the OpenTofu HTTP backend and
  on the cloud block. `tofu plan` and `tofu apply` with the remote lock stop
  before they read or write state.
- A write of state through OpenTofu that holds no lock. A client that has the
  permission to write without the lock, and the switches that allow it, is still
  refused. A client that holds its own lock keeps writing, see below.
- The start of a mutating update of the Pulumi CLI for the stack. `pulumi up`,
  `pulumi refresh` and `pulumi destroy` stop. `pulumi stack import` stops too,
  because the server runs an import as an update of the stack. A restore of an
  earlier state version stops for the same reason. `pulumi preview` still works.
- `pulumi stack rename`. A rename that the lock allowed would leave the state
  under the old name, because the import that a rename needs next meets the lock.
- The restore of an earlier state version of a workspace, unless the caller
  holds the lock of the workspace.
- The removal of the stack or of the workspace.
- The restore of a stack or workspace that someone removed, when a manual lock
  covers the place it comes back to: its own lock, the lock of its collection, or
  the lock of its namespace. The server refuses the restore and the stack or
  workspace stays removed. A lock stops every change, and a restore is a change.
- The start of a job for the stack or workspace, and the dispatch of a queued
  phase, see "Jobs" below.

A manual lock does not stop reads. It does not stop `pulumi preview`, the edit of
the tags of a stack, or the release of a tool lock. A tool force-unlock, for
example `tofu force-unlock`, ends the lock of the tool only.

A tool that already holds its own lock on the stack or workspace keeps it. The
lock holder can still write state and release the lock. A request that is in
progress at the moment of the lock can pass: the check and the request run in
separate transactions. The next request meets the lock. This holds for the lock
requests of the tools and for the rename and the move of a stack or workspace out of a
collection. The check of the removal of a stack or workspace, the check of a
move out of a collection and the check of a restore run in the transaction of
the write, so a lock taken before
the write commits is seen.

## Jobs

The job rules below are part of the job domain, but the server does not run them
yet. The server has no job store, and the clock that lets a running phase pass
the lock is not connected. Until a job store is connected, no job is started or
dispatched, so the lock check of a job has no effect. The check of a job token
on a lock request fails closed: a job token never passes a manual lock now.

When a job store is connected, a manual lock stops the jobs of the units that it
covers:

- A new job for a covered unit does not start. The job is queued with the wait
  reason `locked`. This holds for a plan job too.
- A queued phase of a covered unit does not dispatch. A job that waits for the
  next phase, for example the apply phase after an approved plan, stays queued
  with the reason `locked`.
- A queued job keeps its queue timeout. It expires after the same time as a job
  that waits for another reason. A job that waits behind a lock does not wait for
  ever.
- A phase that already runs, runs to its end. The lock request of OpenTofu and
  the start of a Pulumi update that this phase makes with its job token pass the
  lock, when the phase started before the lock. A job whose phase started after
  the lock, and a job of another unit, meet the lock.
- Break-glass does not pass a manual lock. `namespace:break-freeze` lets a job
  start in a change freeze. It gives no way past a manual lock. The way out is the
  force-unlock.

When the lock goes, the schedule `job.reconcile` dispatches the queued job at its
next run. It runs each minute.

## What the tools print

The text names the stack or workspace in the words of the tool, the holder, the time and the
reason. Then it names the route that the holder uses to remove the lock. The Pulumi CLI prints, for a stack in a locked collection:

```text
stack acme/web/prod is in collection acme/payments, which has a manual lock by alice since 2026-10-19T10:00:00Z: change window closed. To remove the lock, the holder sends DELETE /api/dustpan/v1/namespaces/acme/collections/payments/lock.
```

For a lock on the stack itself the text starts with `stack acme/web/prod has a
manual lock by`, and the route is `/api/inventory/unit-lock?unit=pulumi:acme/web/prod`.
For a lock on the namespace it starts with `stack acme/web/prod
is in namespace acme, which has a manual lock by`, and the route is
`/api/dustpan/v1/namespaces/acme/lock`. The TFE-compatible API gives the same
text with the word `workspace`. A person with `unit:manage` removes a lock of
another holder with the force-unlock route of the object (see "Remove a lock").

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
shows the lock on the stack or workspace.

## The object of a lock

A manual lock is on one stack, one workspace, one collection or one namespace.

- A lock on a stack or workspace covers that stack or workspace. A rename keeps
  the lock. A job phase that started before the lock passes it after a rename
  of its unit.
- A lock on a collection covers each stack and workspace that is in the
  collection now. A stack or workspace that joins the collection is covered from
  that moment. One that leaves the collection is not covered any more.
- A lock on a namespace covers each stack and workspace of the namespace, in a
  collection or not, and each one that someone creates later.
- While a namespace has a lock, the server creates no OpenTofu workspace in it.
  The first `LOCK` or `POST` of a new workspace answers 423, and the server
  creates nothing. A job whose phase started before the lock can still create
  its workspace, as it can still lock the workspaces that exist.
- A stack or workspace can have its own lock, be in a locked collection and be in
  a locked namespace. The nearest lock shows first: its own lock, then the lock
  of its collection, then the lock of its namespace. Removing the nearest lock
  shows the next one.
- A stack or workspace moves out of a locked collection only for a person with
  `unit:manage` on it. The move ends the coverage of the collection lock, so the
  server refuses it for a person who only has `unit:move`. The audit entry of
  an allowed move names the lock.

Deleting an empty collection removes its lock. A collection that someone creates
again under the same name has no lock.

## Roles

| Step | Action | Object | Roles |
| --- | --- | --- | --- |
| Take a lock | `unit:lock` | The stack, workspace, collection or namespace | `maintainer`, `admin` |
| Remove your own lock | `unit:lock` | The stack, workspace, collection or namespace | `maintainer`, `admin` |
| Remove the lock of another person (force-unlock) | `unit:manage` | The stack, workspace, collection or namespace | `maintainer`, `admin` |
| Read a lock | `unit:read` for a stack or workspace, `collection:read` for a collection, `namespace:read` for a namespace | The stack, workspace, collection or namespace | Every role that reads |

For a namespace, the role must be granted on the namespace itself. A grant on one
collection or one unit does not reach the lock of the namespace.

`namespace:break-freeze` does not pass a manual lock. The way out is the
force-unlock.

## Take a lock

The body gives the reason. The reason is required and has at most 500
characters. It has no line break and no other control character. The body must
be valid UTF-8, with no lone surrogate escape such as `\ud800`. The server
refuses such a body with 400 and does not change the text.

For a stack or workspace, `PUT /api/inventory/unit-lock?unit=<unit reference>`:

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

For a whole namespace, `PUT /api/dustpan/v1/namespaces/<namespace>/lock`:

```sh
curl -X PUT -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"reason": "release freeze"}' \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/lock"
```

Each call locks one object. A lock on a namespace covers all of its stacks and
workspaces, so it is the way to lock many at once.

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

`kind` is `unit`, `collection` or `namespace`. `object` is the reference of a stack or
workspace (`<tool>:<namespace>/<name>`), `<namespace>/<collection>`, or the name
of the namespace. `holder` is the subject of the person that took the
lock. `since` is an RFC 3339 time in UTC.

The server answers:

- 400 when the body has no reason, an unknown field, or a reason that is not
  valid.
- 404 when the stack, workspace, collection or namespace does not exist.
- 409 when the object already has a manual lock. The message names the holder
  and the reason. Remove the lock, or ask the holder to remove it.

## Read a lock

`GET` on the same path answers the lock, or 404 when the object has no manual
lock. A stack or workspace in the inventory list has the field `manualLock` when a
lock covers it, for a lock on itself and for a lock on its collection. The field has the same
shape as the answer above. The field `object` names the object that holds the
lock. The field `lock` of the stack or workspace stays the lock of the tool.

## Remove a lock

The holder sends `DELETE` on the same path. The answer is 204. The answer is
409, and the lock stays, in these two cases:

- Another person holds the lock.
- The lock changed while the server removed it. A person ended the lock and took
  a new one in the same moment. The new lock stays. Read the lock again, then
  decide again.

A person with `unit:manage` on the stack, workspace, collection or namespace removes the
lock of another person with a force-unlock. The answer is 204:

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/inventory/unit-lock/force-unlock?unit=pulumi:acme/web/prod"

curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/collections/payments/lock/force-unlock"

curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/lock/force-unlock"
```

A force-unlock answers 404 when the object has no manual lock, or does not
exist. It answers 409 when the lock changed while the server removed it. The new
lock stays. Read the lock again, then decide again.

## Audit

Each lock and each removal writes an audit entry in the same transaction as the
change. The entry names the person, the action, the object and the reason. A
removal names the holder, the reason and the time of the lock that it ends.
A force-unlock has the action `unit:manage` and the version `force-unlock`.
