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

A write with an `Idempotency-Key` header fails with 400 and the code
`request.idempotency-key-unsupported`: no route of this API takes the key.

## The trigger document

| Field | Content |
| --- | --- |
| `id` | The trigger ID, which starts with `trg-` |
| `namespace` | The name of the namespace |
| `unit` | `id` (the stable ID), `tool` (`Pulumi` or `OpenTofu`) and `name` of the unit that the trigger starts |
| `kind` | `review-request-push`, `schedule` or `follows` |
| `frequency` | For `schedule`: `preset` or `cron` |
| `source` | For `follows`: `id`, `tool` and `name` of the source unit. `name` is empty when the inventory no longer knows the unit. |
| `enabled` | `false` for a trigger that starts nothing |
| `lastSaver` | The subject of the person who saved the trigger last |
| `needsNewSave` | `true` when the last saver no longer holds the actions of a start. Save the trigger again. |

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

The error body has `message`, `code` (the status) and, for a refusal that a
program can tell, `errorCode`. A refusal of a save that closes a loop of units
also has `units`: the units of the loop in order, each with `id`, `tool` and
`name`. The first and the last unit are the same unit.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object with known fields, or is larger than 16 KiB |
| 400 | `trigger.invalid` | A value fails validation: the kind, the frequency, the source, the reason or the location |
| 400 | `request.idempotency-key-unsupported` | The request has an `Idempotency-Key` header |
| 403 | `trigger.saver-denied` | The person who saves or fires has no principal record that holds the actions of a start, or the last saver lost access |
| 403 | | The caller lacks an action, or the stack or workspace to change does not exist |
| 404 | | The object does not exist or the caller cannot read it |
| 409 | `trigger.cycle` | The `follows` trigger closes a loop of units |
| 409 | `trigger.disabled` | A fire of a trigger that is off |
| 409 | `trigger.fire-conflict` | A second fire within the minute with another reason or location |
| 409 | `namespace.fenced` | The namespace moved to another region during the call. Try again. |
| 409, 422, 429 | `job.frozen`, `job.unsupported`, `job.limit` | The refusal of the job service |
| 403 | `job.policy-denied`, `job.gate-enforced`, `job.agent-denied` | The refusal of the job service |

An error text never holds the text of an internal failure.
