# Error codes of the tool-neutral APIs

Each error answer of a tool-neutral API has the stable field `errorCode`. A
program selects its action from `errorCode` and never from the text of
`message`. A code never changes its meaning, and the server keeps every code.
The text of `message` can change.

This page lists the codes that all tool-neutral APIs share, and the codes of
the inventory API, the context API and the audit API. The pages of the
notification destinations API and the profiles API list their own codes.

## The body

```json
{"code": 404, "message": "deleted unit not found", "errorCode": "unit.deleted-not-found"}
```

`code` is the HTTP status. It is not the machine code. Some answers have more
fields:

- `errorArgs` holds the arguments of the code. An answer with the header
  `Retry-After` has the argument `retryAfterSeconds`, the same number of
  seconds.
- `reauthenticate` holds the path that starts a new sign-in, for the refusal
  of a sign-in that is too old.

Each error answer has the header `Cache-Control: no-store`.

## Shared codes

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `namespace.invalid-name` | The name of the namespace is not valid |
| 400 | `instance.invalid-id` | The instance ID of a `DELETE /api/server/instances/{id}` request has a character that is not allowed, or a wrong length |
| 401 | `auth.unauthenticated` | The request has no valid token and no valid session |
| 403 | `access.denied`, `access.ip-not-allowed`, `session.csrf`, `session.other-site` | See the error codes of a 403 answer |
| 404 | `namespace.not-found` | No namespace with this name |
| 404 | `instance.not-found` | No instance version row has this ID |
| 400 | `storecheck.invalid-target` | The target of a store check is not `blobs` or `backup` |
| 404 | `storecheck.not-found` | No store check has this ID, or the ID has a form that the server never makes |
| 409 | `storecheck.running` | Another store check runs. One check runs at a time |
| 409 | `storecheck.cooldown` | A store check on this target ended less than 15 minutes ago |
| 409 | `storecheck.refused` | The installation is in maintenance mode or a restore runs |
| 409 | `storecheck.no-target` | The server has no store for the target, for example no backup target |
| 409 | `namespace.fenced` | A failover moved the namespace during the request |
| 409 | `request.idempotency-key-lost` | A newer request took the `Idempotency-Key` before this request ended |
| 421 | `namespace.not-home` | The namespace has another primary installation |
| 429 | `auth.rate-limited` | Too many failed sign-ins from this address |
| 500 | `server.internal` | An error that the server does not classify. The message is `internal error` |
| 503 | `maintenance.in-maintenance` | The installation is in maintenance mode |
| 503 | `schema.binary-below-minimum` | The binary version of this server is below the minimum binary version of the database schema, so this server cannot write. The arguments are `binaryVersion` and `minimumVersion` |
| 503 | `worker.absent` | No worker claimed or renewed a claim for 2 lease periods of the worker. The server refuses a request that starts a durable operation. Start a worker with `opendustpan serve --components worker` |

## Inventory API

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.invalid-query` | A query value is missing or not valid, for example `organization` or the `unit` of a move, or a `pageToken` belongs to another query |
| 400 | `request.body-invalid` | The body is not the body that the route needs |
| 400 | `request.invalid-object` | The object of the request is not valid |
| 400 | `request.idempotency-key-unavailable` | The server cannot keep `Idempotency-Key` answers |
| 400 | `collection.invalid-name` | The name of the collection is not valid |
| 400 | `unit.invalid-name` | The name of the unit does not follow the name rules of its tool |
| 400 | `view.invalid` | The saved view is not valid |
| 400 | `lock.reason-required` | A lock needs a reason |
| 400 | `lock.invalid-target` | The lock target is not valid |
| 400 | `audit.reason-invalid` | The reason is not valid |
| 403 | `view.not-person` | The caller is not a person: sign in as a person to use saved views |
| 403 | `gate.denied`, `gate.limited` | A gate or an access limit refused a state write |
| 404 | `unit.not-found` | No unit, no unit that the caller may read, or the unit is not in the collection |
| 404 | `unit.deleted-not-found` | No deleted unit with this name |
| 404 | `collection.not-found` | No collection with this name |
| 404 | `view.not-found` | No saved view with this name |
| 404 | `lock.target-not-found` | The object of the lock does not exist |
| 404 | `lock.not-locked` | The object has no lock |
| 409 | `collection.already-exists` | A collection with this name exists |
| 409 | `collection.not-empty` | The collection holds units |
| 409 | `collection.changed` | The collection changed during the request |
| 409 | `unit.no-collection` | The unit is in no collection |
| 409 | `collection.move-out-locked` | The unit cannot leave a locked collection |
| 409 | `unit.name-taken` | A live unit holds the name: send a new name in the restore body |
| 409 | `view.already-exists` | A saved view with this name exists |
| 409 | `view.limit` | The person has the maximum number of saved views |
| 409 | `lock.already-locked` | The object has a lock |
| 409 | `lock.not-holder` | The caller does not hold the lock |
| 409 | `lock.changed` | The lock changed during the request |
| 409 | `lock.locked` | A lock refuses the change |
| 409 | `gate.rule-failed` | A gate failed to evaluate, so the server refuses the state write |
| 501 | `unit.history-unavailable` | The inventory store keeps no change log |
| 503 | `gate.unavailable` | The gates could not decide a state write |

## Context API

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body has no known section |
| 400 | `request.invalid-query` | The `include` query value is not valid |
| 400 | `context.invalid` | The context is not valid |
| 400 | `context.unknown-section` | The context has a section that the server does not know |
| 404 | `context.object-not-found` | No object, or no object that the caller may read |
| 404 | `context.not-set` | The object has no context from the API to clear |
| 409 | `context.field-from-code` | The field comes from the code of the unit: change it in the `opendustpan` output |
| 409 | `context.unit-deleted` | The unit is in the deleted state: restore it first |

## Audit API

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.invalid-query` | The `organization` query value is missing, or `page[number]` or `page[size]` is not a positive number |
| 400 | `request.invalid-path` | The sequence in the path is not a positive number |
| 400 | `request.invalid-object` | The object of the request is not valid |
| 404 | `namespace.not-found` | No namespace with this name |
| 404 | `audit.entry-not-found` | No entry at this sequence |
| 501 | `audit.entry-read-unavailable` | The store cannot read one entry: list the entries |
