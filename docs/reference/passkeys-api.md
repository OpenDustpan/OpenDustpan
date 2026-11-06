# Passkeys API

The passkeys of a person are the WebAuthn credentials that the person added to
the own account. This page describes the four routes that start and end the
add of a passkey, list the passkeys and remove one. The console uses these
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

There is no route to rename a passkey and no route to sign in with a passkey.

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

## Errors

An error has the fields `code` (the HTTP status), `message` and `errorCode`. A
client selects its text from `errorCode`.

| Status | `errorCode` | When |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object with the known fields |
| 400 | `mfa.passkey-invalid` | The name is not valid, or a value of the response is not valid |
| 400 | `mfa.ceremony-not-open` | No open ceremony has the challenge of the response: it expired, it was used already, or it never existed |
| 400 | `mfa.ceremony-failed` | The response failed a check of the protocol: the origin, the relying party ID, the signature, a flag or the user verification |
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
| 503 | `mfa.unavailable` | The store of the passkeys cannot answer |

## Audit

Every add, every removal and every refusal writes an entry in the scope
`@server`. The actions are `passkey.added`, `passkey.removed`,
`passkey.add-refused`, `passkey.remove-refused` and `passkey.list-refused`. A
refusal of the step-up check writes an entry for the action
`caller:manage-sign-in` with the outcome `failure`. No entry holds a challenge,
a key or a response.
