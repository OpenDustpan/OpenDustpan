# Notification destinations API

The paths are under `/api/dustpan/v1/namespaces/{namespace}`. The API names a
notification destination a `sink`. Every route needs a token or a console
session. The error body has the fields `code`, `message` and `errorCode`, and
the field `errorArgs` when an error has arguments.

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
| `GET /subscriptions` | `namespace:read` | the namespace | 200, the list |
| `GET /subscriptions/{subscription_id}` | `namespace:read` | the namespace | 200, one subscription |
| `POST /subscriptions` | `namespace:manage-notifications` | the namespace | 201, the subscription |
| `PUT /subscriptions/{subscription_id}` | `namespace:manage-notifications` | the namespace | 200, the subscription |
| `DELETE /subscriptions/{subscription_id}` | `namespace:manage-notifications` | the namespace | 204 |
| `GET /subscriptions/{subscription_id}/deliveries` | `namespace:read` | the namespace | 200, one page of the history |
| `POST /deliveries/{delivery_id}/redeliver` | `namespace:manage-notifications` | the namespace | 204 |

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
| `hostNeedsUpdate` | `true` when the stored host of a `webhook` or `chat` destination fails the host check of a write. For a `webhook` destination, the destination still sends. It keeps the legacy webhook compatibility. For a `chat` destination, this field does not promise delivery. The chat sender sends only when the host of the URL passes the chat host check at send time. A URL that the chat host check rejects does not send. A change of the URL needs a host that passes the allowlist. Absent otherwise. |
| `target` | The address of an `email` destination, or the host of a `webhook` or of a `chat` webhook. The answer never holds the path or the query of a URL. |
| `urlSet` | `true` for a `webhook` or a `chat` destination that has a sealed URL item. `false` for a destination that has no URL item, and for a `webhook` that an upgrade left with no URL. It does not say that the server can open the URL. |
| `createdBy` | The subject that made the destination |
| `version` | Starts at 1 and grows by 1 with each change |
| `signingKey` | Only in the answer of the create of a `webhook`. 64 hex digits. |

## Create

The body is one JSON object. The server refuses a field that it does not know.
The body has at most 64 KiB.

| Kind | Fields |
| --- | --- |
| `webhook` | `kind`, `name`, `target` (an `https` URL on port 443, or on a port that the operator allows). The host is an ASCII DNS name or an IP address, with an optional numeric port. The URL is a secret: the server seals it, and the answer shows only its host. |
| `chat` | `kind`, `platform`, `name`, `url` (the incoming webhook URL, on the same ports), and `accessToken` for a platform that needs one |
| `email` | `kind`, `name`, `target` (one address) |

The server sends the path of a chat `url` as you write it, with its encoded
bytes. A path segment that decodes to a slash, a backslash, a dot, two dots, a
question mark, a number sign or a control character answers 400
`sink.invalid`, so that the server never sends a token to another path on the
host.

`accessToken` is write-only. The server seals it like the URL as a second
secret item, and no answer, audit entry or log holds it. It has 1 to 4096
printable ASCII characters and no space. A platform that takes no access token
refuses the field, and a platform that needs one refuses a create without it.
Both answer 400 `sink.access-token`.

A field that a kind does not take, for example `url` for a `webhook`, answers
400. The answer has the header `Cache-Control: no-store` and the header
`Location`.

The answer of the create of a `webhook` holds the signing key. A replay of the
same `Idempotency-Key` answers 409 `request.idempotency-key-replayed-secret`
with the ID of the destination.

The operator sets the ports besides 443 with `--destination-ports`
(`DUSTPAN_DESTINATION_PORTS`). The server checks the port when a person makes
or changes a destination, before each test, and in the sender. A URL on another
port answers 400 `sink.invalid`.

## Change

The body has `version` and `name`, and may have `target` (a `webhook` or an
`email` destination), or `url` and `accessToken` (a `chat` destination). A
`target` of a `webhook`, a `url` and an `accessToken` are new secrets under a
new reference. A new `url` keeps the access token, and a new `accessToken`
keeps the URL. The signing keys of a
`webhook` keep their references. The kind and the platform of a destination do not
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
error. A destination of a kind or a chat platform with no sender answers 422
`sink.no-sender`. The platform `slack` has a sender. The host of a `slack`
destination is `hooks.slack.com` or `hooks.slack-gov.com`, and any other host
answers 400 `sink.platform-host` when you make or change the destination. The
platform `mattermost` has a sender. Its host is any host that the outbound
rules allow, because you host the server. The platform `teams` has a sender for
the webhook URL of a Workflows flow. Its host is one or more names before
`logic.azure.com` or `api.powerplatform.com` (or a Microsoft cloud for
government or China domain from the Power Automate list), with no port or with
the port 443. The flow answers 202 when it got the card. That answer does not
prove that Teams showed the card. The platform `matrix` has a sender. Its `url`
is the room, `https://<homeserver>/_matrix/client/v3/rooms/<room ID>` with the
room ID percent-encoded (`!` is `%21` and `:` is `%3A`). The server sends the
room in that form and then adds `/send/m.room.message/<notification ID>`. The
`accessToken` is the access token of the account that posts. The host is any
host that the outbound rules allow. A destination whose sealed URL is missing,
does not open, or has another host than the `target` answers 422 `sink.no-url`,
and the server sends nothing. Set the URL again with a change. See
`docs/guides/slack-notifications.md` for the message.

A name that resolves to an address that the outbound rules refuse gives the
class `temporary`, the same as a name that does not resolve. The class
`refused` is for a URL that the rules refuse before any lookup.

### Limits of a test

| Limit | Value |
| --- | --- |
| One destination | 1 test in 10 seconds |
| One namespace | 10 tests in 1 minute |

A test over a limit answers 429 `sink.test-rate-limited` with the header
`Retry-After` (whole seconds) and `errorArgs.retryAfterSeconds`. The server
sends no message and writes no audit entry. A test of a destination whose
address the rules refuse counts against the limits too. The limits count in
the database, so they hold for all server instances of one installation. Each
limit uses fixed windows, and a refused test counts in its window too.
The windows start at fixed times, so two adjacent windows can permit two tests
of one destination and twenty tests of one namespace in a short interval: ten
tests at the end of one minute window and ten at the start of the next.

When the server cannot read or write the count, it sends no message and
answers 503 `sink.test-limit-unavailable`. A test that the server cannot count
would be a test without a limit. When the database does not answer, the server
returns 503 `namespace.unavailable` with the header `Retry-After` (5 seconds),
also when the counter of the test limit wraps that error.

The server sends the message only after these steps: this installation is the primary
installation of the namespace, the limits take the test, the address passes the outbound rules,
and the first audit entry is written under the fence of the namespace. A
namespace of another installation answers 421 `namespace.not-home`. A moved fence
answers 409 `namespace.fenced`.

## Subscriptions

A subscription sends the notifications of the namespace that match its filter
to one destination of the same namespace. A list, a read and the history of
deliveries need `namespace:read`. A create, a change, a delete and a redelivery
need `namespace:manage-notifications`.

| Field | Content |
| --- | --- |
| `id` | `sub-...` |
| `namespace` | The namespace |
| `sink` | The ID of the destination. It is a destination of the same namespace. |
| `filter.types` | Notification types. An entry that ends in `.*` selects a family of types. An empty list selects every type. At most 100 entries. |
| `filter.objects` | Object references. An entry that ends in `/*` selects a prefix. An empty list selects every object. At most 100 entries. |
| `filter.minSeverity` | `info`, `warning` or `critical`. The answer shows `info` for a filter with no severity. |
| `state` | `active` or `suspended`. A suspended subscription sends nothing. |
| `reason` | Why the subscription sends nothing. Absent for an active subscription. |
| `createdBy` | The subject of the last create or change. The server checks the access of this subject before each delivery. |
| `version` | Starts at 1 and grows by 1 with each change |

### Create a subscription

The body has `sink`, and may have `filter` with the fields `types`, `objects`
and `minSeverity`. The subscription starts `active`. The answer has the header
`Location`.

A `sink` that the namespace does not hold answers 422
`sink.subscription-sink-not-found`. The answer is the same for an unknown
destination and for a destination of another namespace.

### Change a subscription

The body has `version`, `sink`, `state` and may have `filter`. A change replaces
the whole subscription. A `filter` that you leave out becomes the filter that
selects everything. `state` is `active` or `suspended`. A change to `active`
resumes a subscription that the server or a person suspended. The change sets
`createdBy` to the subject of the request, so the server checks the access of
that subject from then on. A `version` that is not the stored version answers
409 `sink.subscription-conflict`.

A change of the kind, the target or the secrets of a destination suspends its
active subscriptions with the reason `the destination of the sink changed`. A
person resumes them with this route.

### Delete a subscription

A delete deletes the subscription and its pending deliveries. The other
deliveries stay in the history.

## Deliveries

`GET /subscriptions/{subscription_id}/deliveries` answers one page of the
deliveries of the subscription, newest first. The query parameters:

| Parameter | Content |
| --- | --- |
| `pageSize` | 1 to 200. The default is 50. |
| `pageToken` | The `nextPageToken` of the page before, for the same subscription |

The answer has `deliveries` and `nextPageToken`. `nextPageToken` is empty on the
last page. The history reaches back at most 1000 deliveries, and the server
enforces that count. A page that would go past it ends there and has no next
page. A token names the last delivery of the page before. The next page starts
after the delivery with that `nextAt` and ID. A page never skips or repeats a
delivery while no delivery changes: a retry or a new send changes `nextAt`. A
token for another subscription answers 400 `request.page-token-invalid`.

| Field | Content |
| --- | --- |
| `id` | `dlv-...` |
| `notification` | The ID of the notification |
| `subscription` | The ID of the subscription |
| `sink` | The ID of the destination |
| `contact` | Only for a delivery to the personal contact of a person |
| `principal` | The subject whose access decides the delivery |
| `state` | `pending`, `delivered`, `abandoned` or `skipped` |
| `attempts` | The number of attempts |
| `nextAt` | When the next attempt is due |
| `lastError` | `refused`, `permanent` or `temporary`. Absent when the last attempt did not fail. |
| `deliveredAt` | Absent until the receiver accepts the delivery |
| `version` | The version of the delivery row |

A delivery holds no address, no secret and no text of an error.

### Send a delivery again

`POST /deliveries/{delivery_id}/redeliver` makes a delivery that stopped
(`delivered`, `abandoned` or `skipped`) pending again, with no attempts. The
dispatcher then sends it. The body is empty. A delivery can be sent again
only while its subscription exists, is `active`, sends to the destination of
the delivery, and has the `principal` of the delivery as `createdBy`. A
delivery from before a change of the destination or of the subscription never
goes to the new place. A delivery that is pending, or that fails these
conditions, answers 409 `sink.delivery-not-redeliverable`. An unknown delivery,
and a delivery of another namespace, answer 404 `sink.delivery-not-found`.

## Error codes

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `sink.invalid` | A field is not valid, or the outbound address rules refuse the URL or its port |
| 400 | `sink.access-token` | The platform takes no access token, needs one and got none, or the token is not valid |
| 400 | `sink.platform-host` | The host of the chat URL is not a webhook host of the platform |
| 400 | `request.body-invalid` | The body is not one JSON object with known fields |
| 403 | `access.denied`, `access.ip-not-allowed`, `session.csrf`, `session.other-site` | See the error codes of a 403 answer |
| 400 | `sink.subscription-invalid` | A field of a subscription is not valid: the sink, the state or the filter |
| 400 | `request.invalid-query` | `pageSize` is not a number from 1 to 200 |
| 400 | `request.page-token-invalid` | `pageToken` does not belong to this subscription |
| 404 | `sink.not-found` | No destination with this ID in the namespace |
| 404 | `sink.subscription-not-found` | No subscription with this ID in the namespace |
| 404 | `sink.delivery-not-found` | No delivery with this ID in the namespace |
| 409 | `sink.subscription-conflict` | The subscription changed since you read it |
| 409 | `sink.delivery-not-redeliverable` | The delivery is pending, or its subscription does not send it now |
| 422 | `sink.subscription-sink-not-found` | The namespace holds no destination with the ID in `sink` |
| 409 | `sink.conflict` | The destination changed since you read it |
| 409 | `namespace.fenced` | A failover moved the namespace during the request |
| 421 | `namespace.not-home` | The namespace has another primary installation |
| 429 | `sink.test-rate-limited` | A limit of the test route refuses the test |
| 503 | `sink.test-limit-unavailable` | The server cannot count the tests, so it refuses the test |
| 422 | `sink.no-sender` | The server has no sender for the kind or the chat platform of the destination |
| 422 | `sink.no-url` | The server cannot use the sealed URL of the destination: set the URL again |

The text of a `sink.*` error is fixed. It never holds a part of the request.

## Audit entries

| Action | When |
| --- | --- |
| `sink:create` | A create |
| `sink:update` | A change |
| `sink:delete` | A delete |
| `subscription:create` | A create of a subscription |
| `subscription:update` | A change of a subscription, a suspend and a resume included |
| `subscription:delete` | A delete of a subscription |
| `delivery:redeliver` | A redelivery |
| `sink:test` | A test. The reason `started` marks the entry before the send. The next entry has the class of the result, and its outcome is `failure` when the message was not delivered. |

The entry has the object `<namespace>` and the ID of the destination, the
subscription or the delivery. It holds no URL, name, filter or secret. A replay
of a request with the same `Idempotency-Key` writes no second entry.
