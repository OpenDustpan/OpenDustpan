# Triggers API

A trigger is a rule of one stack or workspace that starts a plan of it. The
triggers API saves, reads, lists, replaces and deletes triggers, and fires one by
hand. The [triggers guide](../guides/run-triggers.md) explains the kinds, the last
saver and the checks.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. The caller signs in as for the other APIs, with an
access token or a console session. `{unit_id}` is the stable ID of the stack or
workspace.

| Method and path | Actions | Answer |
| --- | --- | --- |
| `GET /namespaces/{namespace}/units/{unit_id}/triggers` | `namespace:read`, `unit:read` | The triggers of the unit, ordered by ID |
| `POST /namespaces/{namespace}/units/{unit_id}/triggers` | `namespace:read`, `unit:read`, `unit:manage`, `unit:run-plan` | The new trigger, with status 201 |
| `GET /namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` | `namespace:read`, `unit:read` | One trigger |
| `PUT /namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` | `namespace:read`, `unit:read`, `unit:manage`, `unit:run-plan` | The replaced trigger |
| `DELETE /namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` | `namespace:read`, `unit:read`, `unit:manage` | Status 204 |
| `POST /namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire` | `namespace:read`, `unit:read`, `unit:run-plan` | The new job, with status 201 |

The server serves these routes only when it has the stores of the job service
(see the [jobs API](jobs-api.md)) and the principal records.

The server checks the actions in this order. A caller who lacks `namespace:read`
or `unit:read` gets 404, the same answer as for an object that does not exist.
This holds for a namespace or a stack or workspace that does not exist, a unit
of another namespace than the path, a deleted unit, a trigger that does not exist
or belongs to another unit, and an ID that is not valid. A caller who lacks
another action gets 403. A `follows` trigger needs `unit:read` on its source too.

Only the fire route takes an `Idempotency-Key` header. It holds 1 to 255
printable ASCII characters. A key that starts with the prefix `drift-` is not
valid: the fire route answers 400 with `request.idempotency-key-invalid`. Two
fires by one person with one key give one job,
also in two different minutes. The answer does not carry the header
`Idempotent-Replayed`. A write to any other route with the header fails with 400
and the code `request.idempotency-key-unsupported`.

## The trigger document

| Field | Content |
| --- | --- |
| `id` | The trigger ID, which starts with `trg-` |
| `namespace` | The name of the namespace |
| `unit` | `id` (the stable ID), `tool` (`Pulumi` or `OpenTofu`) and `name` of the unit that the trigger starts |
| `kind` | `review-request-push`, `schedule` or `follows` |
| `frequency` | For `schedule`: `preset` or `cron`, and `expression`. `expression` is the five-field cron expression, in UTC, that the schedule runs at. For a preset, the minute and the hour come from an offset that the unit ID sets, so they can differ between units. A cron answer repeats the `cron` value in `expression`. |
| `source` | For `follows`: `id`, `tool` and `name` of the source unit. `name` is empty when the inventory no longer knows the unit, or when you do not hold `unit:read` on the source. |
| `enabled` | `false` for a trigger that starts nothing |
| `lastSaver` | The subject of the person who saved the trigger last |
| `needsNewSave` | `true` when the last saver no longer holds the actions of a start, or no longer holds `unit:read` on the source of a `follows` trigger. Also `true` when the source of a `follows` trigger no longer exists. Such a trigger starts no plan. Save the trigger again. |

## Save and replace

The body is one JSON object of at most 16 KiB with the fields `kind`,
`frequency`, `source` and `enabled`. A field that is not in the list fails the
request with 400. A body cannot set the unit or the last saver.

| Field | Content |
| --- | --- |
| `kind` | Required. `review-request-push`, `schedule` or `follows` |
| `frequency` | `schedule` only: `preset` (`hourly`, `every-6-hours`, `daily` or `weekly`) or `cron` |
| `source` | `follows` only: `{"id": "<unit ID>"}`, a unit of the same namespace |
| `enabled` | Optional, default `true` |

A replace keeps the unit and the kind of the trigger. A body for another unit
or another kind fails with 400 and the code `trigger.invalid`.

## Fire

The optional body has `reason` (at most 200 characters) and `location`
(`repository`, `ref`, and optionally `connection`, `directory` and `revision`).
The answer is `{"job": {"id": "job-..."}}` and a `Location` header with the path
of the job.

## Errors

The error body has `message`, `code` (the status) and `errorCode`, the stable
code of the refusal. A code that has arguments also has `errorArgs`. A refusal of a save that closes a loop of units
also has `units`: the units of the loop that you can read, in order, each with
`id`, `tool` and `name`. A unit on which you do not hold `unit:read` is not in
`units`, and `message` gives only the number of such units. The first and the
last unit of a loop are the same unit.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object with known fields, or is larger than 16 KiB |
| 400 | `trigger.invalid` | A value fails validation: the kind, the frequency, the source, the reason or the location |
| 400 | `request.idempotency-key-unsupported` | A route other than the fire route got an `Idempotency-Key` header |
| 400 | `request.idempotency-key-invalid` | The `Idempotency-Key` header of a fire is not one valid key, or it starts with `drift-` |
| 403 | `trigger.saver-denied` | The person who saves or fires has no principal record that holds the actions of a start, or the last saver lost access. A new principal that has the subject of the last saver does not count as the last saver |
| 401 | `auth.unauthenticated` | The request has no valid credential |
| 429 | `auth.rate-limited` | Too many sign-ins from the address of the request failed |
| 403 | `access.denied` | The caller lacks an action, or the stack or workspace to change does not exist |
| 403 | `access.ip-not-allowed`, `session.csrf`, `session.other-site`, `gate.limited`, `gate.rule-failed` | Another cause of a 403, for example an address outside the IP allowlist of the namespace |
| 404 | `trigger.not-found` | The namespace, the unit or the trigger does not exist, or the caller cannot read it. One code answers all of these |
| 421 | `namespace.not-home` | The namespace has another primary installation |
| 503 | `maintenance.in-maintenance` | The installation is in maintenance mode |
| 409 | `lock.locked` | A manual lock refuses the request. `errorArgs` names the `holder`, the time `since`, the locked object (`lockedKind`: `namespace`, `collection`, `stack` or `workspace`, and `locked`) and the refused unit (`refusedKind`: `stack` or `workspace`, and `refused`). Each name has the form `namespace` or `namespace/name`, up to 401 characters for a unit. It never holds the reason of the lock |
| 500 | `server.internal` | The server failed with an error that it does not classify |
| 409 | `trigger.cycle` | The `follows` trigger closes a loop of units |
| 409 | `trigger.disabled` | A fire of a trigger that is off |
| 409 | `trigger.fire-conflict` | A second fire with the same key, or without a key in the same minute, with another reason or location |
| 409 | `namespace.fenced` | The namespace moved to another installation during the call. Try again. |
| 409, 422, 429 | `job.frozen`, `job.unsupported`, `job.limit` | The refusal of the job service |
| 403 | `job.policy-denied`, `job.gate-enforced`, `job.agent-denied` | The refusal of the job service |

An error text never holds the text of an internal failure.
