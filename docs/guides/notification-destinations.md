# Connect a webhook and send a test message

This guide shows how to make a notification destination in a namespace, how
to check its signature in your receiver, and how to send a test message to it.

A notification destination is a place that gets notifications of a namespace.
The server knows three kinds: an email address, a chat webhook (Slack, Microsoft
Teams, Mattermost or Matrix) and a signed webhook. The API names a destination
a `sink`.

**This version sends one message to a destination: the test message.** The
server does not yet send other notifications to a destination by itself. The
signed webhook has a sender. The kinds `chat` and `email` have none yet, so
you can make and read their destinations, but the test route answers
`sink.no-sender` for them.

The paths in this guide are under `/api/dustpan/v1/`. See
`docs/reference/notification-destinations-api.md` for every field and error.

## Before you start

| What you do | Action | Object |
| --- | --- | --- |
| Make, change, delete or test a destination | `namespace:manage-notifications` | the namespace |
| List or read a destination | `namespace:read` | the namespace |

The built-in roles `admin`, `account-administrator` and `site-administrator`
hold `namespace:manage-notifications`. The roles `viewer`, `auditor`,
`secret-viewer`, `writer` and `maintainer` do not hold it. Each role that holds
`namespace:read` can list and read the destinations of its namespace.

Your receiver needs an `https` URL with a public host name on port 443. The
server refuses a URL that points to your own machine or to a private network,
and a URL with another port. See "Which addresses the server accepts".

## Make a signed webhook destination

`POST /api/dustpan/v1/namespaces/<namespace>/sinks` makes a destination.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"kind":"webhook","name":"Operations","target":"https://hooks.example.com/dustpan"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/sinks
```

The answer is 201. It holds the ID of the destination, its version, and the
**signing key**:

```json
{
  "id": "snk-...",
  "namespace": "acme",
  "kind": "webhook",
  "name": "Operations",
  "target": "hooks.example.com",
  "urlSet": false,
  "createdBy": "ana",
  "version": 1,
  "signingKey": "9f2c...64 hex digits"
}
```

**Save the signing key now.** The server shows it once. No route answers it
again, and a replay of the same `Idempotency-Key` does not answer it either:
the replay answers 409 with the ID of the destination. If you lose the key,
make a new destination.

The server seals the key with the data key of the namespace. A list or a read
of the destination shows no key.

The URL of the webhook is a secret, because its path or its query can hold a
token. The server accepts it when you make or change the destination. No
answer shows it again: `target` holds only the host. To see the URL, read it
from your own records.

## Check the signature in your receiver

Each request to a webhook has three headers:

| Header | Content |
| --- | --- |
| `Dustpan-Notification-Id` | The ID of the notification, `ntf-...` |
| `Dustpan-Timestamp` | The time of the request in Unix seconds |
| `Dustpan-Signature` | `v1=` and the hex HMAC-SHA256 of `<id>.<timestamp>.<body>` |

The key of the HMAC is the text of the signing key, as the server answered it.
Your receiver must:

1. Compare the signature in constant time.
2. Refuse a timestamp that is more than 5 minutes from its own clock.
3. Drop a second request with the same notification ID.

The body is JSON with the fields `schemaVersion`, `id`, `type`, `scope`,
`object`, `objectId`, `severity`, `summary`, `links`, `attributes`,
`dedupeKey`, `at` and `sequence`.

## Send a test message

`POST /api/dustpan/v1/namespaces/<namespace>/sinks/<id>/test` sends one fixed
message to the destination.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/sinks/snk-.../test
```

The message has the type `sink.test` and says that it is a test. No event
happened. The answer is 200 and tells you what happened:

| `class` | `delivered` | Meaning |
| --- | --- | --- |
| (none) | `true` | The receiver answered with a 2xx status. |
| `refused` | `false` | The server does not send to this address. |
| `permanent` | `false` | The receiver refused the message, for example with a 404. Check the URL and the signing key. |
| `temporary` | `false` | The message did not arrive: a timeout, a network error, or a 5xx status. Try again later. |

The `message` is a fixed text for each class. **The answer never holds the URL,
the answer of the receiver or the text of a network error.** To find out why a
test failed, look at the log of your receiver.

The test waits at most 10 seconds for the receiver. It makes one attempt and
does not retry.

The server limits the tests: one test for each destination in 10 seconds, and
10 tests for each namespace in one minute. A test over a limit answers 429. It
sends no message and writes no audit entry. The header `Retry-After` and the
field `errorArgs.retryAfterSeconds` give the wait in seconds. A new
`Idempotency-Key` does not change this.

The server also refuses to send a test when the namespace has another home
region, or when a failover moves the namespace during the request. It writes
the first audit entry of the test before it sends the message. If that write
fails, the server sends nothing.

## Connect a chat webhook

A chat destination holds the incoming webhook URL of a chat platform.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"kind":"chat","platform":"slack","name":"Ops channel","url":"https://hooks.slack.com/services/T000/B000/XXXX"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/sinks
```

The `platform` is `slack`, `teams`, `mattermost` or `matrix`. The URL holds
the token of the webhook, so the whole URL is a secret. The server seals it.
A read of the destination shows the platform, the name and the **host** of the
URL (`hooks.slack.com`), and `"urlSet": true`. It never shows the URL.

To change the URL, send a `PUT` with a new `url`. The server stores the new
URL as a new secret and deletes the old one. A change of the platform is a
new destination.

## Change and delete a destination

`PUT /api/dustpan/v1/namespaces/<namespace>/sinks/<id>` changes the name, and
the `target` of a webhook or the `url` of a chat destination. Send the
`version` that you read. If another request changed the destination since,
the answer is 409 and nothing changes.

A change of the `target` of a webhook keeps the signing key. The new receiver
must use the key of the destination. To give the new receiver a new key,
delete the destination and make it again.

`DELETE /api/dustpan/v1/namespaces/<namespace>/sinks/<id>` deletes the
destination and its secrets. The answer is 204.

## Which addresses the server accepts

The server checks the URL when you make or change a destination, again before
a test, and again on each connection. It refuses:

- a URL that is not `https`
- a URL with user information or a fragment
- a URL with a port other than 443, unless the operator allows the port with
  `--destination-ports`
- `localhost`, and a host that is a number
- an address in a loopback, private, link-local or reserved range, for example
  `127.0.0.1`, `10.0.0.5` and `169.254.169.254`
- a host name that resolves to such an address when the server connects

The server follows no redirect. An answer with a 3xx status is a `permanent`
result.

The result of a test does not tell a host name that resolves to a refused
address from a host name that does not resolve. Both give the class
`temporary`. Only a URL that the server refuses before it looks up the name,
for example a literal private address or a port that is not allowed, gives the
class `refused`.

## Audit entries

Each create, change and delete writes one audit entry of the kind `change` in
the namespace. The actions are `sink:create`, `sink:update` and `sink:delete`.
A test that sends a message writes two entries with the action `sink:test`:
one with the reason `started` before the server sends, and one with the class
of the result in its reason, for example `class: temporary`, after it. Each
entry names the destination by its ID. It never holds the URL, the name, a
signing key or the answer of a receiver.

## The Idempotency-Key header

Each route that changes data takes the header `Idempotency-Key`. A retry with
the same key and the same body does the operation once. A test with a key does
not send a second message on a retry. See `docs/reference/inventory-api.md`
for the rules of the header.
