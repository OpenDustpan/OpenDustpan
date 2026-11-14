# Access tokens

This guide shows how to make, list, rotate and revoke access tokens. It covers
the three kinds of token, the expiry and the inactivity rules, the warnings
that a token gives before it expires, and the token policy of the installation.

An access token is a secret string that a client sends as
`Authorization: Bearer <token>`. A token acts as its principal, with every
grant that the principal has. The server stores only a digest of the secret.
It shows the secret once, when the token is made or rotated.

The examples use `$TOKEN` for a token and the host `dustpan.example.com`.

A console session is the cookie `__Host-dustpan_session`, and a change sends the
`X-Dustpan-CSRF` header with it. The console sends both. A script that holds only an
access token cannot make or rotate a token, or change the token policy.

## The kinds of token

| Kind | Who holds it | Made by | Routes |
| --- | --- | --- | --- |
| Personal | A person, for the CLI or a script | The person, after a fresh sign-in | `/api/server/caller/tokens` |
| Service | A service principal, for a machine | A caller with `principal:manage-tokens` on the service principal | `/api/server/service-tokens` and `/api/server/principals/{principal}/tokens` |

A workload does not get a stored token from these routes. A workload gets a
token from an exchange, and that token lasts for the time that the exchange
sets.

A token of one kind cannot be used on a route for the other kind. The answer is
`400` with the code `token.wrong-kind`.

## Make a personal token

A person makes a personal token for itself. The create needs a fresh sign-in
in the console. A token cannot make another token. The answer is `403` with the
code `mfa.fresh-step-required`.

Make the token in the console. A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. The body of the create is:

```json
{"name":"laptop-cli","expiresAt":"2027-06-30T00:00:00Z"}
```


The answer is `201` with the new token and its `secret`. Copy the secret now.
Later reads show no secret.

| Field | Rule |
| --- | --- |
| `name` | Required. At most 100 bytes. No control or format characters, and no leading or trailing spaces. |
| `expiresAt` | Optional, in RFC 3339. It must be after the time of the request, and within the maximum lifetime. Without it, the lifetime is 90 days, or the maximum lifetime if that is shorter. |

## List and read tokens

A person lists its own tokens with `GET /api/server/caller/tokens`. The list
ignores a `principal` value. An administrator with `server:read-tokens` lists
every token with `GET /api/server/tokens`. A service principal's tokens are listed
with `GET /api/server/principals/{principal}/tokens`.

These query values work on the lists:

| Query | Meaning |
| --- | --- |
| `status` | `active`, `expiring` or `ended`. `expiring` is an active token that expires within 14 days. `ended` is a token that is revoked, disabled or expired. Any other value gets `400`. |
| `principal` | On the administrator list, only the tokens of this principal. |
| `limit` | From 1 to 1000. The default is 100. |
| `cursor` | The `nextCursor` of the page before. |

The lists are ordered with the newest token first. Each page shows `total`, which
counts every match, and `nextCursor`.

A token has these fields:

| Field | Meaning |
| --- | --- |
| `id` | The ID, with the prefix `tok-`. |
| `principalID` | The principal that holds the token. |
| `name`, `kind` | The name, and `personal` or `service`. |
| `status` | `active`, `expired`, `revoked` or `disabled`. |
| `createdAt`, `expiresAt`, `lastUsedAt` | The times. `lastUsedAt` is `null` until the first use. |
| `revokedAt`, `disabledAt` | The time of the revoke or the disable. |
| `expiresInDays` | The days left, rounded up, for an active token. A token in its last day answers 1. |
| `expiryWarning` | `none`, `soon` or `imminent`. See [Expiry warnings](#expiry-warnings). |
| `replacedBy` | The ID of the token that replaced this one, after a rotation. |
| `createdBy` | The principal that made the token. |

A personal token has no read route of its own, so use the list to find it.
An administrator reads one token with `GET /api/server/tokens/{id}`.

## Make a service token

A caller with `principal:manage-tokens` on the service principal makes its token in the
console. A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. The body of the service-token create is:

```json
{"principalID":"prn-...","name":"ci-deploy","expiresAt":"2027-06-30T00:00:00Z"}
```


The principal must be a service principal. For another kind, the answer is `400`.
Use the same body for the route under the principal, without `principalID`:

The principal token create uses the same fresh sign-in. The body is:

```json
{"name":"ci-deploy"}
```


Both routes answer `201` with the secret.

### The reach of a service principal

A caller that holds `principal:manage-tokens` on the installation makes or
rotates a token of a service principal only when the reach of that principal
is inside the reach of the caller. The reach of a principal is the union of
its grants, the grants of its groups, and the grants of each service principal
that it can act as.

The server compares the two reaches. If the principal holds an action on an
object that the caller does not hold, the server refuses the change. The
answer is `403` with the code `token.outside-reach`. The message names the
principal and each action that the caller does not hold, with its object:

```
principal "prn-x" holds actions that you do not hold: server:backup on the installation
```

The message names an action and its object only when the caller holds the read
action of that object. All the other actions go into one phrase at the end
that says `objects that you cannot read`, so the message does not tell how many
objects the caller cannot read. The audit entry of the refusal names
every missing action with its object.

A holder of `principal:manage-tokens` on the service principal itself is not
compared. A site administrator holds every action, so it passes. A rotation
follows the same rule.

The same compare protects the grant that lets its holder act as the principal.
A grant of a delegation role, such as `token-manager`, on a service principal
needs a granter that holds the reach of that principal. A membership add to a
group that holds such a grant needs the same. Otherwise the answer is `403`,
and the change does not happen. The refusal names the principal only when the
caller holds `server:read-principals`. A revoke, and a removal of a member,
take access away, so they never run this compare.

## Revoke a token

A revoke answers `204`. The token stops working at once, and its record stays
for the history.

| Route | Action |
| --- | --- |
| `DELETE /api/server/caller/tokens/{id}` | The person who owns the token, with `token:manage-own` |
| `DELETE /api/server/tokens/{id}` | `server:manage-tokens` for a personal token, or `principal:manage-tokens` on the principal for a service token |
| `DELETE /api/server/principals/{principal}/tokens/{id}` | `principal:manage-tokens` on the principal |

A person cannot revoke another person's token on its own routes. The answer is
`404`. Revoke a token that is already revoked gets `409` with the code
`token.ended`.

## Rotate a token

A rotation makes a new token, and it sets an end time for the old one.

Rotate the token in the console. A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. Rotation of a service token or a principal token has the same rule.


The routes are `POST /api/server/caller/tokens/{id}/rotate` for a personal token,
`POST /api/server/service-tokens/{id}/rotate` for a service token, and
`POST /api/server/principals/{principal}/tokens/{id}/rotate` for a token of one
principal. Each answers `201`.

The new token keeps the name and the kind. The body takes these optional
fields:

| Field | Meaning |
| --- | --- |
| `oldTokenEndsAt` | When the old token stops working. It must be at or after now, and at or before the old expiry. |
| `expiresAt` | The expiry of the new token. Without it, the new token gets the default lifetime. |

Without `oldTokenEndsAt`, the old token works for 24 hours after the rotation,
or until its own expiry if that is earlier. Set `oldTokenEndsAt` to now to end
the old token at once. A token that is already replaced, or that is not active,
gets `409` with the code `token.ended`.

The answer holds the new token, with its `secret`, and `previous`, with the old
token and its end time.

## Expiry and inactivity

Each token has an expiry. The server refuses a token whose expiry is after the
maximum lifetime. Once the expiry passes, the token is expired and the server
refuses it. The server does not write a separate expired state.

A token that no one uses for a set period is disabled. The period is set by the
token policy, see [The token policy](#the-token-policy). A use is a request that the
server accepts with the token. The server writes `lastUsedAt` at most once a minute.
A token that was never used counts from its creation.

The disable applies to inactive tokens. The sweep also disables an inactive
person account when the policy allows it. The break-glass accounts and the
temporary administrators in use are exempt.

A token that the server refuses gets `401` with the code `auth.unauthenticated`.
The reason stays in the server log. The answer does not name the reason, so
an expired, revoked, disabled or replaced token look the same to the client.

## Expiry warnings

Before a token expires, the server gives a warning in three places:

- The `expiryWarning` field of the token. The value is `soon` when 14 days or
  less are left, and `imminent` when 3 days or less are left. Otherwise it is
  `none`.
- The response header `Dustpan-Token-Expires-At` on each answer to a request
  that uses the token. The value is the expiry time in RFC 3339. The header is
  not set for the bootstrap administrator token or for a console session.
- The CLI prints one warning for each command when the header is present. The
  warning does not name the token.

This release does not send an email or a console banner about an expiring token.
To find the tokens that expire soon, list them with `status=expiring`.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/tokens?status=expiring"
```

## The token policy

The token policy sets the lifetimes and the inactivity rules of the installation.
It has these routes:

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/server/token-policy` | `server:read-tokens` | `200` with the policy |
| `PATCH /api/server/token-policy` | `server:manage-token-policy`, with a fresh sign-in | `200` with the policy |
| `POST /api/server/token-policy/sweep` | `server:manage-token-policy` | `200` with the records that the sweep disabled |

The policy has these fields. A `PATCH` takes any subset of them:

| Field | Default | Range | Meaning |
| --- | --- | --- | --- |
| `maxLifetimeSeconds` | 31536000 (365 days) | 1 second to 100 years | The longest life of a new token. A later expiry is refused. A token made before a lower maximum keeps its expiry. |
| `inactivitySeconds` | 7776000 (90 days) | 0, or 1 hour to 100 years | The period without use after which a token is disabled. `0` turns the rule off. |
| `disableTokens` | `true` | `true` or `false` | Disable the inactive tokens. |
| `disableAccounts` | `true` | `true` or `false` | Disable the inactive person accounts. |
| `exchangeLifetimeSeconds` | 3600 | 60 to 86400 | The life of a token that a workload gets in an exchange. |
| `updatedAt` | `null` | Read only | The time of the last change. |

The field `disableBreakGlass` is no longer accepted. A value of `true` gets `400`.

Change the policy in the console. The change needs a fresh sign-in, so a stored access token
gets `403`. The body is:

```json
{"maxLifetimeSeconds":15552000,"inactivitySeconds":5184000}
```


The server runs the inactivity rule once an hour. To run it now, post to the
sweep route:

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/server/token-policy/sweep
```

The answer holds `tokens`, the IDs of the disabled tokens, and `principals`, the
accounts that were disabled. The sweep does nothing when `inactivitySeconds` is
`0`, or when both disable settings are `false`.

Each disabled record writes one entry in the audit log, with the reason. Each
change to the policy writes one entry, with the object `token-policy`.

## Session settings

The console session has its own settings, at `GET /api/server/session-policy`
and `PATCH /api/server/session-policy`. A session ends after 15 minutes without
activity, or after 12 hours in all. The fresh sign-in window of a sensitive
change is 900 seconds by default. Set the window to `0` to turn the check off.

## Errors

| Code | Status | Meaning |
| --- | --- | --- |
| `auth.unauthenticated` | `401` | The token is unknown, expired, revoked, disabled or replaced. |
| `mfa.fresh-step-required` | `403` | A create or a rotation of a token, or a change of the token policy, needs a fresh console sign-in. An access token gets this answer. |
| `token.caller-not-person` | `403` | The caller has no person record. |
| `token.outside-reach` | `403` | The reach of the service principal is not inside the reach of the caller. |
| `token.not-found` | `404` | The token is unknown, or it belongs to another person. |
| `token.ended` | `409` | The token is revoked, replaced or not active. |
| `token.already-exists` | `409` | The name or the secret is already used. |
| `token.wrong-kind` | `400` | The route is for the other kind of token. |
| `request.invalid-query` | `400` | A `status`, `limit` or `cursor` value is not valid. |
| `request.body-invalid` | `400` | The body is not valid, or the policy holds `disableBreakGlass`. |
| `auth.rate-limited` | `429` | Too many sign-in attempts. |
