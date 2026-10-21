# Notification destinations API

The paths are under `/api/dustpan/v1/namespaces/{namespace}`. The API names a
notification destination a `sink`. Every route needs a token or a console
session. The error body has the fields `code`, `message` and `errorCode`.

See `docs/guides/notification-destinations.md` for the steps.

## Routes

| Route | Action | Object | Answer |
| --- | --- | --- | --- |
| `GET /sinks` | `namespace:read` | the namespace | 200, the list |
| `GET /sinks/{sink_id}` | `namespace:read` | the namespace | 200, one destination |
| `POST /sinks` | `namespace:manage-notifications` | the namespace | 201, the destination |
| `PUT /sinks/{sink_id}` | `namespace:manage-notifications` | the namespace | 200, the destination |
| `DELETE /sinks/{sink_id}` | `namespace:manage-notifications` | the namespace | 204 |
| `POST /sinks/{sink_id}/test` | `namespace:manage-notifications` | the namespace | 200, the result |

Each route that changes data takes the header `Idempotency-Key`. A `GET`
route with the header answers as without it. A server with no store of replay
records answers 400 `request.idempotency-key-unsupported` to a request with the
header.

A write carries the fence of the namespace. A stale fence answers 409
`namespace.fenced`. A namespace that does not exist answers 404
`namespace.not-found`.

## The destination

| Field | Content |
| --- | --- |
| `id` | `snk-...` |
| `namespace` | The namespace |
| `kind` | `webhook`, `chat` or `email` |
| `platform` | For `chat`: `slack`, `teams`, `mattermost` or `matrix`. Not in the answer for another kind. |
| `name` | 1 to 100 bytes |
| `target` | The address of an `email` destination, the URL of a `webhook`, or the host of a `chat` webhook |
| `urlSet` | `true` for a `chat` destination. It says that a URL exists. |
| `createdBy` | The subject that made the destination |
| `version` | Starts at 1 and grows by 1 with each change |
| `signingKey` | Only in the answer of the create of a `webhook`. 64 hex digits. |

## Create

The body is one JSON object. The server refuses a field that it does not know.
The body has at most 64 KiB.

| Kind | Fields |
| --- | --- |
| `webhook` | `kind`, `name`, `target` (an `https` URL) |
| `chat` | `kind`, `platform`, `name`, `url` (the incoming webhook URL) |
| `email` | `kind`, `name`, `target` (one address) |

A field that a kind does not take, for example `url` for a `webhook`, answers
400. The answer has the header `Cache-Control: no-store` and the header
`Location`.

The answer of the create of a `webhook` holds the signing key. A replay of the
same `Idempotency-Key` answers 409 `request.idempotency-key-replayed-secret`
with the ID of the destination.

## Change

The body has `version` and `name`, and may have `target` (a `webhook` or an
`email` destination) or `url` (a `chat` destination). A `url` is a new secret
under a new reference. The kind and the platform of a destination do not
change. A `version` that is not the stored version answers 409 `sink.conflict`.

## Test

The server sends one fixed message with the type `sink.test` through the sender
of the kind of the destination. The answer:

| Field | Content |
| --- | --- |
| `delivered` | `true` when the receiver accepted the message |
| `class` | Not set for a delivered message. Else `refused`, `permanent` or `temporary`. |
| `message` | A fixed text for the class |

The answer never holds the URL, the answer of the receiver or the text of an
error. A destination of a kind with no sender answers 422 `sink.no-sender`.

## Error codes

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `sink.invalid` | A field is not valid, or the outbound address rules refuse the URL |
| 400 | `request.body-invalid` | The body is not one JSON object with known fields |
| 404 | `sink.not-found` | No destination with this ID in the namespace |
| 409 | `sink.conflict` | The destination changed since you read it |
| 422 | `sink.no-sender` | The server has no sender for the kind of the destination |

The text of a `sink.*` error is fixed. It never holds a part of the request.

## Audit entries

| Action | When |
| --- | --- |
| `sink:create` | A create |
| `sink:update` | A change |
| `sink:delete` | A delete |
| `sink:test` | A test. The outcome is `failure` when the message was not delivered. |

The entry has the object `<namespace>` and the ID of the destination. It holds
no URL, name or secret.
