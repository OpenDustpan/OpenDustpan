# Keep a break-glass account

A break-glass account is an account of one named person that does not
depend on the identity provider. It is the way in when the identity
provider is not available. Keep two enabled accounts, unless a self-hosted
operator sets the recovery mode `none`. The server enforces a lower bound
only: at least one enabled account stays.

## Sign in and the alert

A break-glass account has no password. It signs in with a passkey and a reason
that the person types. See
[Sign in with a passkey](passkeys.md#sign-in-with-a-passkey) for the two
requests.

Each break-glass sign-in does these things in one database transaction:

- It starts the session.
- It writes an audit entry in the scope `@server` with the action
  `session:sign-in`, the reason, and the change `breakGlass` set to `true`.
- It writes the notification `break-glass.used` to the outbox of the
  installation. The severity is `critical`, and the recipients are the
  administrators. The notification holds the ID of the session and the ID of
  the account.

A sign-in without a valid reason, or with a response that fails a check, starts
no session and writes no notification. It writes an audit entry with the real
cause.

The review of the use after the fact is not part of this version. The
notification `break-glass.used` and the audit entry are the records that the
review reads.

The reason has 1 to 500 characters. The server refuses a reason with a
control character or a hidden format character, for example U+202E (right-to-left
override) or U+200B (zero-width space), because the administrators read the
reason in the alert and a hidden character could show other text than the
stored text.

## Add an account

A site administrator adds a break-glass account, its first passkey and its
grant in three requests. Each needs a recent sign-in.

1. Make the passkey with an authenticator. Send `POST
   /api/server/break-glass/accounts` with the person and the passkey:

   ```json
   {
     "subject": "break-glass:recovery",
     "displayName": "Recovery",
     "name": "recovery-key",
     "credentialID": "<base64url>",
     "publicKey": "<base64url>",
     "transports": ["usb"]
   }
   ```

   The credential ID and the public key are base64url without padding. The
   answer is `201` with the account and its passkeys. The route accepts the
   add when no working account exists, and it does not lower any count.

2. Give the account the role `site-administrator` on the installation with
   `POST /api/server/grants`. A break-glass account with a passkey but no
   administration grant signs in, but it cannot change anything. The working
   count includes only an account with a grant.

If the add route fails after it writes the account, the account has no
passkey and the count does not include it. List the accounts, delete the
account with `DELETE /api/server/principals/{id}`, and add it again. If the
grant request fails, retry it: the account has a passkey but no grant, and the
count does not include it.

The action `server:manage-break-glass` allows the add and the rotation. The
action `server:read-break-glass` allows the list. Only a site administrator
holds the two actions.

## List the accounts and the count

`GET /api/server/break-glass/accounts` returns each break-glass account with
its passkeys, and the live count:

```json
{
  "accounts": [{"id": "prn-...", "subject": "break-glass:recovery", "enabled": true, "passkeys": []}],
  "working": 1,
  "missing": 1,
  "floor": 2
}
```

`working` is the count of the working paths at the request time. `missing` is
the count that the installation needs to reach `floor`, and `0` at or above
it. The count reads the rows at the request time, so an account that stops
working, by a suspended passkey or a revoked grant, leaves the count without
a write.

## Rotate a passkey

`POST /api/server/break-glass/accounts/{id}/rotate` replaces one passkey of
an account with a new one. The rotation needs a recent sign-in. The body names
the old passkey and the new one:

```json
{
  "oldPasskeyID": "pky-...",
  "name": "next-key",
  "credentialID": "<base64url>",
  "publicKey": "<base64url>",
  "transports": ["usb"]
}
```

The server writes the new passkey, removes the old one and writes the audit
entry in one database transaction. A failed write keeps the old passkey. Two
rotations of one account run one after the other, and the second rotation
sees the new passkey. An account that is not a break-glass account, or a
passkey that the account does not hold, answers `404` with the error code
`break-glass.not-found`. The answer of a change is `204`.

## The lock timeout

The setting `principal.breakGlassLockSeconds` holds the number of whole
seconds that a guarded change may wait for the lock of the break-glass rows.
Its default is `5`, and it takes `1` to `60`. `GET /api/dustpan/v1/settings`
shows its effective value and the layer that gave it.

The guard of a change takes the lock of the server audit scope, so the
changes that count the break-glass accounts run one after the other. A change
that the guard refuses answers `409` with the error code
`principal.no-working-break-glass` and writes nothing. A sign-in takes no such
lock, so a break-glass sign-in works while a guarded change holds it.

## The session limit

A session of a break-glass account ends 1 hour after the sign-in. The limit is
absolute: no use of the session moves it, and it is shorter than the maximum
length of the session policy. The cookie of the session ends at the same time.
The sessions of other people keep the limits of the session policy.

The setting `session.breakGlassMaxLength` holds the limit. Its default is
`1h0m0s`. It takes 5 minutes to 12 hours, as a Go duration in whole seconds.
`GET /api/dustpan/v1/settings` shows its effective value and the layer that
gave it. A change to a longer limit needs a fresh sign-in. A value that the
server cannot read gives the default of 1 hour, never no limit.

## The alert after failed assertions

Each failed passkey response for a credential of a break-glass account counts
for that account. At 10 failures the server writes an alert to the
administrators, with the severity `warning`. One account gets at most one alert
in each hour. The alert is not a lock: the owner can sign in at once, and the
count blocks no one. The count is by account and not by credential, so it
needs no credential ID from the person who sends the response.

## The rule

The server refuses to disable or delete the last enabled break-glass
account. This applies to two requests:

- `POST /api/server/principals/{id}/disable`
- `DELETE /api/server/principals/{id}`

Both answer 409 with the error code `principal.no-working-break-glass` and
this message:

```text
The change leaves too few working break-glass accounts. Add or enable a working break-glass account, then try again. See the break-glass accounts guide.
```

The request changes nothing. The account stays enabled. Before the answer,
the server writes one audit entry in the scope `@server` with the outcome
`denied` and the reason `principal.no-working-break-glass`. When the server
cannot write this entry, it still answers 409 and logs the failure. This
refusal is one of the [code rules](../reference/code-rules.md) that no grant,
setting or administrator can change.

A disabled account does not count as a second account, because it cannot
sign in. The server checks the count in the same database transaction as the
write. When two administrators disable the last two accounts at the same time,
one request succeeds and the other answers 409.

## Disable one of two accounts

You can disable one break-glass account at any time, also when you suspect a
compromise of its credential. Do this while a second enabled account exists.
The disable works in maintenance mode, as every other revocation does. The
server writes an audit entry for it.

The caller must hold every action that the grants of the account give, as
for every other principal. A caller that does not hold them gets 403.

## Replace the last account

1. Make the second break-glass account before you disable the first.
2. Check that the second account can sign in.
3. Disable the first account.

The enable of a disabled account is not a revocation. It stays refused with
503 in maintenance mode.

## Other paths

The inactivity disable never disables a break-glass account. The expiry of a
temporary administrator ends a temporary administrator only, and a temporary
administrator is never a break-glass account.

A restore applies again the disables that you made in maintenance mode.
This can disable more accounts than the backup allows, for example when you
add a third account after the backup and then disable the first two. The
restore keeps the last enabled break-glass account of the restored data
enabled. It skips the disable that would leave none, and it writes an audit
entry in the scope `@server` with the action `maintenance:replay-revocation`,
the outcome `denied` and the reason `principal.no-working-break-glass`. Read
this entry after a restore, then disable the account again when a second
account exists.

A restore also counts the working break-glass accounts before and after it.
A working account is a local person of the installation that is enabled and
not deleted, has a passkey that is not suspended, and holds a current grant
without a condition of `site-administrator` or `account-administrator` on the
installation, directly or through a group. The restore stops with the code
`principal.no-working-break-glass` when it lowers this count and leaves fewer
than two working accounts. A restore that does not lower the count continues,
also when the count is below two.

A break-glass account has no password, and no request of the API can give
it one. A restore writes the rows of the backup, so it checks this rule too.
The restore stops with the code `principal.break-glass-password` and changes
nothing when the restored data holds a break-glass account with a local
password. The command `opendustpan restore` then asks for a backup that
holds no such account.
