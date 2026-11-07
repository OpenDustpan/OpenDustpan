# Passkeys API

The passkeys of a person are the WebAuthn credentials that the person added to
the own account. This page describes the four routes that start and end the
add of a passkey, list the passkeys and remove one. It also describes the two
public routes that sign a person in with a passkey. The console uses these
routes. A script can use them too. For the steps, see
[Add and remove a passkey](../guides/passkeys.md).

The paths of the API start with `/api/dustpan/v1`.

## Paths

| Method and path | Action | Fresh sign-in | Answer |
| --- | --- | --- | --- |
| `POST /me/passkeys/register/begin` | `caller:manage-sign-in` | Yes | 200 with the creation options |
| `POST /me/passkeys/register/finish` | `caller:manage-sign-in` | Yes | 201 with the passkey |
| `GET /me/passkeys` | `caller:read` | No | 200 with the passkeys |
| `DELETE /me/passkeys/{id}` | `caller:manage-sign-in` | Yes | 204 with no body |

Every person that signs in holds both actions with no grant. Every route needs
the console session of a person. A request with an access token answers 403:
the routes that need a fresh sign-in answer with the error code
`mfa.fresh-step-required`, and the list answers with the error code
`mfa.session-required`. A session of a principal that is not a person answers
403 with the error code `mfa.session-required`.

The routes are present only when the server has a public URL that gives a
relying party, and a store that keeps passkeys. Otherwise they answer 404.

There is no route to rename a passkey. The two routes of the
[sign-in with a passkey](#sign-in-with-a-passkey) are public: they need no
session and no action.

## Start the registration

`POST /me/passkeys/register/begin` takes no body. It starts a register
ceremony for the person and the console session of the request, and answers the
creation options in the form of the WebAuthn protocol:

```json
{
  "publicKey": {
    "rp": { "name": "OpenDustpan", "id": "dustpan.example.com" },
    "user": { "id": "...", "name": "alice", "displayName": "Alice Example" },
    "challenge": "...",
    "pubKeyCredParams": [{ "type": "public-key", "alg": -7 }, "..."],
    "timeout": 300000,
    "authenticatorSelection": {
      "residentKey": "required",
      "requireResidentKey": true,
      "userVerification": "required"
    },
    "attestation": "none"
  }
}
```

The challenge has 32 random bytes. It is valid for 300 seconds and works once.
The server keeps the SHA-256 digest of the challenge and never the challenge.
When the person holds passkeys, the options also have `excludeCredentials`
with them, so a device does not add the same key twice. The attestation is always `none`.

A person can start 10 ceremonies in 300 seconds. The 11th answers 429 with the
header `Retry-After` in whole seconds.

## Finish the registration

`POST /me/passkeys/register/finish` takes this body:

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | The name of the passkey: 1 to 100 characters of `a-z`, `A-Z`, `0-9`, `-`, `_` and `.`, not starting with `-` or `.` |
| `response` | object | The credential that the browser made from the creation options, as `JSON.stringify` writes it |

The body has at most 69,632 bytes (68 KiB). The response inside has at most
65,536 bytes. The route reads the body as one JSON object with no unknown field.

The answer is the new passkey:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | The ID of the passkey. It starts with `pky-` |
| `name` | string | The name |
| `transports` | array of strings | The ways that the device connects: `usb`, `nfc`, `ble`, `smart-card`, `hybrid` or `internal` |
| `backupEligible` | boolean | `true` for a passkey that the device can sync |
| `createdAt` | string | The time of the add, as an RFC 3339 string in UTC |
| `lastUsedAt` | string | The time of the last use, or an empty string |
| `suspendedAt` | string | The time of the suspension, or an empty string |

The answer never holds a credential ID or a key.

## List the passkeys

`GET /me/passkeys` answers `{"passkeys": [...]}` with the passkeys of the
person in the order of the add. The members are the ones of the answer above.

## Remove a passkey

`DELETE /me/passkeys/{id}` removes the passkey `id` of the person. A passkey of
another person answers like a passkey that does not exist.

## Sign in with a passkey

A local user or a break-glass account signs in with a passkey and no user name
and no password. The two routes are public. Both need the sign-in of the
console and the passkey service, and both need a budget for the source
address. A server without them answers 404.

| Method and path | Answer |
| --- | --- |
| `POST /sign-in/passkey/begin` | 200 with the request options |
| `POST /sign-in/passkey/finish` | 204 with the session cookie |

`POST /sign-in/passkey/begin` takes no body. It stores a challenge and answers
the request options in the form of the WebAuthn protocol. The options name no
person and no credential (`allowCredentials` is empty), and they require user
verification (`userVerification` is `required`). The challenge has 32 random
bytes, is valid for 300 seconds and works once.

`POST /sign-in/passkey/finish` takes this body:

| Field | Type | Meaning |
| --- | --- | --- |
| `response` | object | The credential that the browser made from the request options, as `JSON.stringify` writes it |
| `reason` | string | The reason of a break-glass sign-in, 1 to 500 characters with no control character and no hidden format character. A local user can omit it, and the server ignores it |

The body has at most 69,632 bytes, and the response inside has at most 65,536
bytes. The server checks the response against the relying party ID and the
origin of the public URL of the server. It never reads the `Origin` header, the
`Host` header or the `Referer` header for this check.

A good sign-in answers 204 and sets the session cookie. The server records it as
a sign-in with the method `passkey` and two factors: the passkey, and the user
verification on the device. A sign-in with a passkey is a fresh sign-in, so the
session can make an access token at once.

### Limits of the source address

Each source address has two budgets. The address is the client address of the
request: an IPv4 address, or the first 64 bits of an IPv6 address.

- Each start of a ceremony spends one unit of the budget of the starts,
  because each start writes a challenge row. The budget is 30 starts, and one
  unit comes back each 2 seconds. A source that used the budget gets 429 at
  once.
- Each failed finish spends one unit of the budget of the failures. 20 failures
  refuse the address for 15 minutes. A source at the limit gets 429 on both
  routes at once, and no ceremony starts.

The address is the client address that the server resolves, never the value of
a request header from a peer that is not a trusted proxy.

The installation holds at most 10,000 open sign-in challenges. Over that count
a start answers 503 with the error code `mfa.unavailable` and stores no row.

The failures count by source address and never by credential. A count by
credential would let a person who knows a credential ID lock the owner out.
The sign-in with a passkey reads no lock of a user name, so a lock of the
password does not stop it.

### Break-glass accounts

A break-glass account signs in with a passkey and the `reason` field. Without a
valid reason the finish answers 400 with the error code
`mfa.break-glass-reason-required` and starts no session. The challenge is used
by then, so the person starts the sign-in again.

A good break-glass sign-in does these things in one database transaction:

- It starts the session.
- It writes the audit entry of the sign-in with the reason and the change
  `breakGlass` set to `true`.
- It writes the notification `break-glass.used` to the outbox of the
  installation, with the severity `critical` and the administrators as the
  recipients. Its dedupe key is `break-glass.used/<session ID>`, and its
  attributes `session` and `principal` name the session and the account.

If one of these writes fails, none of them stays, and the person gets no
session. The session ends 1 hour after the sign-in, or at the limit of the
setting `session.breakGlassMaxLength`.

### Which answer a refusal gets

Every refusal of the finish writes an audit entry and answers 401 with the
error code `mfa.sign-in-failed`, with one text for every cause. These causes
are: an unknown or expired challenge, a challenge used twice, a response with a
wrong origin, relying party ID, signature or type, a response without user
verification, a user handle of another person, a counter that did not grow, a
suspended passkey, a disabled person, and a person that is not a local user and
not a break-glass account. A counter that did not grow also suspends the
passkey.

## Errors

An error has the fields `code` (the HTTP status), `message` and `errorCode`. A
client selects its text from `errorCode`.

| Status | `errorCode` | When |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object with the known fields |
| 400 | `mfa.passkey-invalid` | The name is not valid, or a value of the response is not valid |
| 400 | `mfa.ceremony-not-open` | No open ceremony has the challenge of the response: it expired, it was used already, or it never existed |
| 400 | `mfa.ceremony-failed` | The response failed a check of the protocol: the origin, the relying party ID, the signature, a flag or the user verification |
| 400 | `mfa.break-glass-reason-required` | The sign-in of a break-glass account has no reason, or the reason is not valid text |
| 401 | `mfa.sign-in-failed` | The sign-in with a passkey failed. The answer has one text for every cause |
| 401 | `mfa.fresh-step-required` | The sign-in of the console session is older than the token freshness window. The answer can hold `reauthenticate` |
| 403 | `mfa.fresh-step-required` | The request has an access token and no console session |
| 403 | `mfa.session-required` | The route needs the console session of a person |
| 403 | `mfa.ceremony-wrong-person` | The ceremony belongs to another person or to another console session |
| 404 | `mfa.passkey-not-found` | The person has no passkey with this ID |
| 409 | `mfa.passkey-exists` | A person already holds the credential |
| 409 | `mfa.passkey-limit` | The person holds 20 passkeys. `errorArgs.limit` holds the limit |
| 409 | `local-user.last-credential` | The removal leaves a local user with no password and no passkey, or a break-glass account with no passkey |
| 409 | `local-user.demo-fixed` | The person is the demo user of a dev installation |
| 413 | `request.body-too-large` | The body is longer than `errorArgs.maxBytes` bytes |
| 429 | `mfa.rate-limited` | The person started too many ceremonies. The header `Retry-After` holds the wait in seconds |
| 429 | `mfa.sign-in-source-limit` | The source address holds 20 open sign-in challenges, or its site group (the /48 of an IPv6 address) holds 80. A challenge ends after 5 minutes |
| 429 | `auth.rate-limited` | The source address used the budget of the starts or of the failed responses of the sign-in with a passkey |
| 503 | `mfa.unavailable` | The store of the passkeys cannot answer |

## Audit

Every add, every removal and every refusal writes an entry in the scope
`@server`. The actions are `passkey.added`, `passkey.removed`,
`passkey.add-refused`, `passkey.remove-refused` and `passkey.list-refused`. A
refusal of the step-up check writes an entry for the action
`caller:manage-sign-in` with the outcome `failure`. No entry holds a challenge,
a key or a response.
