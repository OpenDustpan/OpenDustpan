# Local users with a password

This guide shows how to let persons sign in to the console with a user name
and a password, with no identity provider. A local user is one person
principal of the installation. The server keeps the user name and a hash of
the password. No administrator chooses or sees the password of a person.

## What you need

- The public URL of the server, with HTTPS. Set it with `--public-url`
  (`DUSTPAN_PUBLIC_URL`). The console session is a cookie that only an HTTPS
  origin can set, so the password sign-in needs the public URL.
- A SQLite or PostgreSQL store. Both keep the local users.
- An administrator with the action `server:manage-sign-in` on the
  installation. Of the built-in roles, the site administrator holds it.

## Turn the password sign-in on or off

The setting `signIn.localPassword.enabled` turns the password sign-in on or
off. Its value is `"true"`, `"false"`, or empty. When it is empty, the
password sign-in is on when the installation has no identity provider, and
off when it has one. The policy route shows the effective value and where it
comes from (`default` or `set`):

```sh
curl -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example/api/dustpan/v1/local-users/policy
```

```json
{
  "enabled": true,
  "enabledSource": "default",
  "identityProvider": false,
  "minLength": 15,
  "maxLength": 256,
  "requireLettersAndDigits": false,
  "resetLifetimeSeconds": 3600
}
```

While the password sign-in is off, the sign-in, the set-up of a password and
a reset answer `403` with the error code `local-user.password-sign-in-off`.

A password sign-in next to an identity provider needs a second factor. The
server cannot ask for a second factor yet. So when you set the switch to
`"true"` on an installation with an identity provider, each password sign-in
answers `403` with the error code `local-user.second-factor-required`.

## Make a local user

An administrator makes the user. The answer holds a one-use set-up code, and
no password field:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userName":"alice","displayName":"Alice Example","reason":"new team member"}' \
  https://dustpan.example/api/dustpan/v1/local-users
```

```json
{
  "principal": "prn-...",
  "localUser": "lus-...",
  "userName": "alice",
  "subject": "local:lus-...",
  "setUpCode": "<43 characters>",
  "setUpExpiresAt": "2026-11-01T11:00:00Z"
}
```

A user name has 1 to 64 characters: lowercase letters, digits, `.`, `_` and
`-`, with a letter or a digit first. The server refuses each other value. It
does not change a name to make it valid. A sign-in compares the name after it
changes ASCII upper-case letters to lower case.

The subject of the person is `local:` and the ID of the record. The ID never
changes, so a later change of the user name changes no subject, grant or audit
reference. No identity provider can use the provider ID `local`, and no other
principal can have a subject that starts with `local:`.

Give the set-up code to the person on another channel. The code works once.
It ends after the reset lifetime, 1 hour by default. The server keeps only a
SHA-256 digest of the code.

## Set the password

The person sends the code and the new password:

```sh
curl -X POST -H "Content-Type: application/json" \
  -d '{"oneTimeCode":"<set-up code>","password":"<new password>"}' \
  https://dustpan.example/api/dustpan/v1/local-users/redeem
```

The answer is `204`. A code that is not valid, used or expired answers
`400` with the error code `local-user.code-invalid`. A password that a rule
refuses answers `400` with the code of the rule, and the code stays valid.

### Password rules

| Rule | Error code |
| --- | --- |
| At least `password.minLength` characters, 15 by default | `password.too-short`, with the argument `minLength` |
| At most 256 characters and 1024 bytes | `password.too-long` |
| Valid UTF-8 with no control character | `password.form` |
| A letter and a digit, when `password.requireLettersAndDigits` is on | `password.letters-and-digits` |
| Not a common password of the list in the binary. Not the user name, a word of the name of the person, or the product name. Not one character again and again, or a plain sequence. | `password.blocked` |

The server counts Unicode code points after the NFC form, and hashes the NFC
form. It never cuts a password. It has no rule on upper case, digits or
symbols by default, and it never asks for a periodic change. The list of
common passwords is a file in the binary: a check never calls a service
outside the installation.

The server hashes each password with Argon2id: 19 MiB of memory, 2 iterations
and 1 lane. The password hashes of one server share a memory budget of
80 MiB. A sign-in that finds no room in the budget for 2 seconds answers `429`
with the error code `local-user.hash-busy`.

## Sign in

The console sends the user name and the password:

```sh
curl -i -X POST -H "Content-Type: application/json" \
  -d '{"userName":"alice","password":"<password>"}' \
  https://dustpan.example/api/dustpan/v1/sign-in/password
```

A good sign-in answers `204` with the session cookie. The session records the
provider `local`.

Each failure answers `401` with the error code `local-user.sign-in-failed`
and the text "The user name or the password is wrong.": for an unknown user
name, a wrong password, a user with no password and a disabled person. The
answer does not tell which. The audit entry `sign-in.password-failed`
records the real reason. Each failure spends one unit of the password budget
of the source address.

The server refuses a sign-in that another site can send, so that a page of
another site cannot sign your browser in to an account of the attacker. The
request must have the media type `application/json`. When the browser sends
an `Origin` header, it must be one of the allowed origins of the console.
When the browser sends `Sec-Fetch-Site`, it must not be `cross-site`. A
request that fails this check gets the same `401` answer before the server
reads the store, and spends no unit of the password budget. A client that is
not a browser, for example curl, sends neither header and passes with the
JSON media type.

The server refuses a sign-in that another site can send, so that a page of
another site cannot sign your browser in to an account of the attacker. The
request must have the media type `application/json`. When the browser sends
an `Origin` header, it must be one of the allowed origins of the console.
When the browser sends `Sec-Fetch-Site`, it must not be `cross-site`. A
request that fails this check gets the same `401` answer before the server
reads the store, and spends no unit of the password budget. A client that is
not a browser, for example curl, sends neither header and passes with the
JSON media type.

The password budget is separate from the budget of the other sign-ins. The
password sign-in, the use of a set-up or reset code, and the change of the
own password spend it: a wrong password, a code that is not valid and a
wrong current password each spend one unit. When the budget of an address is
empty, these routes answer `429` until it refills.

## Lockout by user name

Each instance on one database shares the counts of failed attempts. The
server adds a failure before it compares the password. A full sign-in
clears the failure counts of its name from its own source, and the counts of
the name from every source. A lock of the name from another source stays.
Two counts apply to each user name:

| Count | Lock |
| --- | --- |
| 10 wrong passwords in a row for one user name from one source (an IPv4 address, or the /64 of an IPv6 address) | that name from that source, for 30 minutes |
| 100 wrong passwords in a row for one user name from every source | that name from every source. The first lock lasts 30 minutes, and each new lock of the name doubles the time, up to 24 hours. After 24 hours with no lock, the next lock lasts 30 minutes again. |

A locked name answers `401` with the same error code as a wrong password,
`local-user.sign-in-failed`. The audit entry `sign-in.password-failed`
records the reason. An unknown user name counts and locks as a known name
does, so a lock tells nothing about a user. The table of the counts holds an
HMAC of the user name, never the name.

A wrong current password of a change of the own password counts too. A good
current password clears the counts of its name, as a full sign-in does, so
only failures in a row lock the name.

A sign-in that answers `429` because the hash budget of the server is full
counts no failure. The server adds the failure only after the hash has its
budget, so an attacker who fills the budget cannot lock the name of a person.

An attacker with 10 or more networks can keep a person out of the password
sign-in for up to 24 hours at a time. An administrator can end each lock.

### Rules in the API

`GET /api/dustpan/v1/local-users/policy` shows the rules that the server
applies, in `lockRules`. The times are in seconds. `limit` is the count of
wrong passwords in a row that sets a lock. `lockSeconds` is the first lock.
Each new lock of the name, less than `resetSeconds` after the earlier lock
ended, lasts twice the lock before, up to `maxLockSeconds`. A rule whose
`maxLockSeconds` is equal to its `lockSeconds` never doubles.
`sourcePrefix` gives the length of the source of the `password` rule, in
bits.

```json
{"lockRules": {
  "password": {"limit": 10, "lockSeconds": 1800, "maxLockSeconds": 1800, "resetSeconds": 0},
  "passwordName": {"limit": 100, "lockSeconds": 1800, "maxLockSeconds": 86400, "resetSeconds": 86400},
  "sourcePrefix": {"ipv4Bits": 32, "ipv6Bits": 64}
}}
```

### Records and alerts

Each lock, each unlock and the end of each lock write an audit entry. The
entry of a lock and the count commit in one transaction. If the
server cannot write the entry or the alert, it does not change the count and
refuses the attempt with an error before it compares the password, and it
writes the error to the log. So a lock never exists without its entry.

The end of a lock has an entry the first time that the name gets an attempt
after the lock ended (`sign-in.name-lock-ended`). The prune of old counts
writes the same entry for each lock that ended with no later attempt, before
it deletes the count. One run writes at most 1000 of these entries, and the
next run writes the rest. The change `locksEnded` of the entry of the run
gives their number.

A new lock of a user name also sends the notification `sign-in.locked` to the
administrators of the installation, with the severity warning. The
notification names the audit object, the scope, the end of the lock and the
level, and never a user name. A lock of one name from one source sends at
most one notification for each name in each hour. The hour is the hour
before the lock, counted in whole minutes, so a second lock of the name
less than one hour after the notification sends none. The audit log still
holds the entry of each lock. A lock of one name from every source sends its
notification at once, each time. Only a principal that can manage the
notifications of the installation can read the notification.

Read the counts of a local user:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example/api/dustpan/v1/local-users/locks?principal=prn-..."
```

```json
{"locks": [{"scope": "password", "source": "192.0.2.1", "lockedUntil": "2026-11-01T12:30:00Z", "failures": 10, "level": 1}]}
```

End every count and lock of the name, with a reason:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"principal":"prn-...","reason":"the person called the help desk"}' \
  https://dustpan.example/api/dustpan/v1/local-users/unlock
```

The answer gives the count of the counts that the unlock cleared. Both routes
need `server:manage-sign-in`. Each action of the person must also be an
action of the administrator, as for a reset. With an access token, the
administrator is the principal of the token. Otherwise both routes answer
`403` with the code `local-user.outside-reach`, and the audit log records the
denial.

### Prune of old counts

Each failed attempt for a new user name adds a count, also for an unknown
name. Every hour, the server deletes at most 10000 counts whose last failure
is 48 hours old or older (the longest lock and the reset time), whose lock
ended and whose level is past its reset time. The next run deletes the rest.
Each run writes the audit entry `sign-in.name-locks-pruned` with the number
of counts, in the transaction of the delete. `GET
/api/dustpan/v1/local-users/policy` shows the effective values in
`lockPrune`:

```json
{"lockPrune": {"intervalSeconds": 3600, "ageSeconds": 172800, "maxRows": 10000, "maxEndedLocks": 1000}}
```

## Change the own password

A signed-in person changes the own password in the console session, with the
current password:

```json
{"currentPassword":"<current password>","newPassword":"<new password>"}
```

Send it to `POST /api/dustpan/v1/me/password`. A session with a recent
sign-in can leave `currentPassword` empty. The change ends every other
session of the person. The browser of the change gets a new session cookie.

The request needs two headers. `Origin` must equal the public URL of the
server. `X-Dustpan-CSRF` must hold the `csrfToken` value of
`GET /api/server/session`. Without `Origin` the server answers `403` with the
code `session.other-site`. Without `X-Dustpan-CSRF` it answers `403` with the
code `session.csrf`.

A wrong current password answers `403` with the error code
`local-user.current-password-wrong`. When the server has no memory for the
check, the change answers `429` with `local-user.hash-busy`, never a wrong
password. A change from a session that another change already ended answers
`401` with `local-user.session-ended`: sign in again. Of two changes from
one session at the same time, one passes and the other gets that answer.

A change that needs a recent sign-in, from a session of the password sign-in
with an old sign-in, answers `401` with no `reauthenticate` link. Sign in
again with the password, and then make the change.

## Reset a password

An administrator makes a reset code for a person who lost the password. The
reset needs a console session with a recent sign-in and a reason.
Set `revokeAccessTokens` to `true` to revoke each access token of the person
too:

```json
{"principal":"prn-...","reason":"the person lost the password","revokeAccessTokens":false}
```

Send it to `POST /api/dustpan/v1/local-users/reset`. The answer holds the
reset code, which the person uses as a set-up code. The reset ends every
session of the person at once. A new code replaces the earlier code of the
same person. An administrator cannot reset the own password: use the change
route.

The reset lets the administrator set a password that signs in as the
person. So the administrator must hold each action that the person holds,
directly, through a group or through a service principal that the
administrator can act as. Otherwise the reset answers `403` with the error
code `local-user.outside-reach`, and the audit log records the refusal. Ask
an administrator who holds each grant of the person.

A break-glass account never has a password. The server refuses a set-up code
and a reset for it with the error code `local-user.break-glass`.

## How a change ends the sessions

Each person has a session generation, a count that starts at 1. Each console
session keeps the generation that its sign-in read. A session authenticates
only while its generation is the stored one. A set-up, a reset and a change
of the password add 1 to the generation in the same transaction as the
change. So each older session of the person ends at its next request, also a
session that a sign-in started while the change ran.

## Settings

| Setting | Default | Values | A change that needs a recent sign-in |
| --- | --- | --- | --- |
| `signIn.localPassword.enabled` | empty | `"true"`, `"false"`, empty | to `"true"` or to empty |
| `password.minLength` | `15` | 12 to 64 | a lower value |
| `password.requireLettersAndDigits` | `false` | `true`, `false` | from `true` to `false` |
| `password.resetLifetime` | `1h0m0s` | 10 minutes to 24 hours, as a Go duration | a longer value |

The settings resource (`GET /api/dustpan/v1/settings`) shows the effective
value and the source of each one.

## Audit entries

| Action | When |
| --- | --- |
| `local-user.created` | An administrator makes a local user. |
| `password.reset-created` | An administrator makes a set-up code or a reset code. The changes record the purpose and whether the reset revoked the access tokens. |
| `password.reset-used` | A person sets a password with a code. |
| `password.reset-refused` | The server refuses a use of a code. The changes record the cause. |
| `password.changed` | A person changes the own password. |
| `password.change-refused` | The server refuses a change of the own password. |
| `password.rehashed` | A sign-in stores a new hash with the current parameters. |
| `sign-in.password-failed` | A password sign-in fails. The changes record the real reason. |
| `sign-in.name-locked` | A count locks a user name. The changes record the scope, the end of the lock and the level. |
| `sign-in.name-lock-ended` | The first attempt for a user name after a lock ended, or the prune of the count of a lock that ended with no attempt. The changes record the scope and the end of the lock. |
| `sign-in.name-unlocked` | An administrator ends the counts and locks of a user name, with a reason. A refused unlock writes a denied entry with the cause. |
| `sign-in.name-locks-read` | The server refuses a read of the counts of a user name. The changes record the cause. |
| `sign-in.name-counts-kept` | A good current password could not clear the counts of its name. The counts stay. |
| `sign-in.name-locks-pruned` | A run of the prune deletes old counts. The subject is the server, and the change `pruned` records the number of counts. The change `locksEnded` records the number of locks that ended with no later attempt. |
| `session:sign-in` | A password sign-in starts a session, with the provider `local`. |

No entry holds a password, a hash or a code.
