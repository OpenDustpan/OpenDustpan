# Add and remove a passkey

A passkey is a key pair that a device of a person holds. The server keeps the
public key and never the private key. A person adds a passkey to the own
account, and removes it again. This guide shows how to do both in the console
and with the API.

A local user or a break-glass account can also sign in with a passkey. See
[Sign in with a passkey](#sign-in-with-a-passkey). The server does not ask for
a passkey as a second factor yet. The page
[Passkeys API](../reference/passkeys-api.md) lists the routes.

## What you need

- A person account with a console session. An access token never adds, lists
  or removes a passkey. A service principal has no passkeys.
- The public URL of the server (`--public-url`, `DUSTPAN_PUBLIC_URL`). Its
  host name is the relying party ID of the passkeys. It must be a host name,
  never an IP address. The scheme is `https`, or `http` on `localhost` only,
  because a browser runs a passkey ceremony only in a secure context.
- A console that the browser opens at the origin of the public URL: the same
  scheme, host name and port. A passkey works only at the origin where the
  browser made it. A server with a public URL that cannot give a relying party
  starts with no passkey routes, and its log has a line that starts with
  `passkeys are off`.
- A SQLite or PostgreSQL store. Both keep passkeys.
- A fresh sign-in. The routes that add or remove a passkey need a console
  session that signed in less than 15 minutes ago. The token freshness window
  of the session policy sets this limit. When the sign-in is older, the
  answer is 401 with the error code `mfa.fresh-step-required`. Sign in again
  and repeat the request.

## Add a passkey

A person adds a passkey in two requests. The first request starts a register
ceremony. The second request ends it.

1. `POST /api/dustpan/v1/me/passkeys/register/begin` answers 200 with the
   creation options. The options hold a random challenge that is valid for 5
   minutes and works once.
2. The browser gives the options to `navigator.credentials.create`. The device
   asks the person to unlock it, for example with a fingerprint or a PIN. The
   server requires this user verification.
3. `POST /api/dustpan/v1/me/passkeys/register/finish` takes the credential that
   the browser made and a name for the passkey. It answers 201 with the new
   passkey.

```sh
curl -X POST "$SERVER/api/dustpan/v1/me/passkeys/register/begin" \
  -H "Cookie: $SESSION_COOKIE" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Sec-Fetch-Site: same-origin"
```

```sh
curl -X POST "$SERVER/api/dustpan/v1/me/passkeys/register/finish" \
  -H "Cookie: $SESSION_COOKIE" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/json" \
  -d '{"name": "laptop", "response": { ... the credential ... }}'
```

```json
{
  "id": "pky-7f3a9c1e2b4d",
  "name": "laptop",
  "transports": ["internal", "hybrid"],
  "backupEligible": false,
  "createdAt": "2026-11-06T09:30:00Z",
  "lastUsedAt": "",
  "suspendedAt": ""
}
```

### The name

The name has 1 to 100 characters. The characters are ASCII letters, digits,
`-`, `_` and `.`. The name does not start with `-` or `.`. A name with a
space, with an accent, with an emoji or with a character that a person cannot
see answers 400 with the error code `mfa.passkey-invalid`. The server checks
the name before the ceremony ends, so a refused name does not use the
challenge.

### Limits

- One person holds at most 20 passkeys. The 21st add answers 409 with the error
  code `mfa.passkey-limit`.
- A person can start 10 register ceremonies in 5 minutes. The next start
  answers 429 with the error code `mfa.rate-limited` and the header
  `Retry-After`. The server writes one audit entry for the refusals of a
  person in one window. Each server process counts for itself, so a database with
  several instances gives a person that many budgets.
- The body of the second request has at most 68 KiB. A larger body answers 413
  with the error code `request.body-too-large`.
- A credential can be added once. The same credential in a second account, or
  twice in one account, answers 409 with the error code `mfa.passkey-exists`.

### What makes the ceremony fail

The ceremony belongs to the person and to the console session that started it.
The second request must come from the same session. The challenge works once:
a second request with the same response answers 400 with the error code
`mfa.ceremony-not-open`, also when the first request failed a check. A response
that fails a check of the passkey protocol answers 400 with the error code
`mfa.ceremony-failed`. The checks include the origin, the relying party ID,
the signature and the user verification. Start the ceremony again after a
failure.

## Sign in with a passkey

A local user signs in with a passkey and does not type a user name or a
password. A local user with no password can sign in only in this way. A
break-glass account signs in only in this way.

1. `POST /api/dustpan/v1/sign-in/passkey/begin` answers 200 with the request
   options. The options name no person.
2. The browser gives the options to `navigator.credentials.get`. The device
   shows the passkeys that it holds for this server. The person chooses one and
   unlocks the device. The server requires this user verification.
3. `POST /api/dustpan/v1/sign-in/passkey/finish` takes the credential that the
   browser made. It answers 204 and sets the session cookie.

```sh
curl -i -X POST "$SERVER/api/dustpan/v1/sign-in/passkey/begin"
```

```sh
curl -i -X POST "$SERVER/api/dustpan/v1/sign-in/passkey/finish" \
  -H "Content-Type: application/json" \
  -d '{"response": { ... the credential ... }}'
```

Both routes are public. The server records the sign-in as a sign-in with two
factors: the passkey, and the user verification on the device. A session of
this sign-in is a fresh session, so the person can make an access token or add
a passkey at once.

### A break-glass account

The body of the second request also holds a `reason`. The person types it, for
example `the identity provider is down`. Without a reason the server answers
400 with the error code `mfa.break-glass-reason-required` and starts no
session. Start again from the first request.

A break-glass sign-in does three things in one database transaction: it starts
the session, it writes the audit entry of the sign-in with the reason, and it
raises the alert `break-glass.used` with the severity `critical` to the
administrators. If one write fails, the person gets no session. The page
[Keep a break-glass account](break-glass-accounts.md) has the rest.

### Why a sign-in fails

Every failure answers 401 with the error code `mfa.sign-in-failed` and one
text, whatever the cause. The audit log has the real cause. These causes fail
the sign-in:

- The challenge expired, or it was used before.
- The response came from another origin or for another relying party ID.
- The device did not check the user (no user verification).
- The passkey belongs to another person than the user handle names.
- The signature counter of the passkey did not grow. The server suspends the
  passkey.
- The passkey is suspended.
- The person is disabled, or is not a local user and not a break-glass
  account.

### Limits

Each source address has a budget of starts and a budget of failed responses.
A start writes a challenge row, so each start spends one unit of the first
budget (30 starts, then one more each 2 seconds). 20 failed responses refuse
the address for 15 minutes. A source at a limit gets 429 with the error code
`auth.rate-limited` at once, with no wait, and no ceremony starts. The address
is the client address that the server resolves: a forwarding header counts only
when a trusted proxy sent it.

Each source address also holds at most 20 open sign-in challenges. An IPv6
source is its /64 range. A site group holds at most 80 open sign-in
challenges, which is 4 times the count of one source. A site group is the /48
of an IPv6 address, and an IPv4 address is its own group. One /48 holds 65,536
ranges of /64, so without the group count one allocation could fill the
10,000 challenges of the installation. With it, a sender needs 125 /48 ranges.
Over either count a start answers 429 with the error
code `mfa.sign-in-source-limit`, stores no row, and writes an audit entry. The
limit keeps one source from using the whole budget of the installation, so a
sign-in from another address, a break-glass sign-in included, still works
while one source floods. Many sources together can still fill the 10,000
challenges of the installation. Then every sign-in with a passkey fails until
challenges end, and a break-glass sign-in with a passkey fails too.

The installation also holds at most 10,000 open sign-in challenges. Over that
count a start answers 503 with the error code `mfa.unavailable` and stores no
row. A challenge ends after 5 minutes. The failures count by source address and never by
credential, so a person who knows a credential ID cannot lock the owner out.
The sign-in with a passkey reads no lock of a user name. A lock of the password
sign-in does not stop it.

## List the passkeys

`GET /api/dustpan/v1/me/passkeys` answers the passkeys of the person in the
order of the add. It does not need a fresh sign-in. The list never holds a
credential ID or a key.

```json
{
  "passkeys": [
    {
      "id": "pky-7f3a9c1e2b4d",
      "name": "laptop",
      "transports": ["internal", "hybrid"],
      "backupEligible": false,
      "createdAt": "2026-11-06T09:30:00Z",
      "lastUsedAt": "",
      "suspendedAt": ""
    }
  ]
}
```

`backupEligible` is `true` for a passkey that the device can sync to other
devices. A passkey with a `suspendedAt` time is suspended because its
signature counter went back. A suspended passkey is a sign of a copied key.
Remove it and add a new one.

A restore of a backup brings back the signature counter of each passkey as it
was at the backup. A use with a counter that is not higher than the restored
counter suspends the passkey.

## Remove a passkey

`DELETE /api/dustpan/v1/me/passkeys/{id}` removes one passkey of the person and
answers 204. It needs a fresh sign-in.

```sh
curl -X DELETE "$SERVER/api/dustpan/v1/me/passkeys/pky-7f3a9c1e2b4d" \
  -H "Cookie: $SESSION_COOKIE" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Sec-Fetch-Site: same-origin"
```

The server refuses a removal in these cases:

- The passkey belongs to another person, or does not exist. Both answer 404
  with the error code `mfa.passkey-not-found`, so the answer tells nothing about
  another person.
- The passkey is the last sign-in credential of a local user that has no
  password, or of a break-glass account. The answer is 409 with the error code
  `local-user.last-credential`. Set a password or add a second passkey first.
- The person is the demo user of a dev installation. The answer is 409 with
  the error code `local-user.demo-fixed`.

## Rename a passkey

This version has no route to rename a passkey. Add a new passkey with the new
name and remove the old one.

## Audit entries

Every add, every removal and every refusal writes an entry in the audit log of
the installation (the scope `@server`). The entries have these actions:

| Action | When |
| --- | --- |
| `passkey.added` | A passkey was added |
| `passkey.removed` | A passkey was removed |
| `passkey.add-refused` | A start or an end of a register ceremony was refused |
| `passkey.remove-refused` | A removal was refused |
| `passkey.list-refused` | A list was refused, for example for an access token |

A refusal of the step-up check writes an entry for the action
`caller:manage-sign-in` with the outcome `failure`. A refusal of the policy
check, for example for a person without the action, writes the entry of any
other refused request. No entry holds a challenge, a key or a response.
