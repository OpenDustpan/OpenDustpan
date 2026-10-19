# Host recovery

This guide shows how to recover when every administrator is lost: the host
command makes a temporary administrator for one named person, straight
through the database.

## When to use it

Use the host command after every administrator is lost, for example when
the last site administrator left and no account holds the
site-administrator role.
It needs access to the host that holds the database: that access is the
credential.
It needs no listener, no identity provider and no DNS.

## Before you start

The command runs only when the installation allows host recovery:

- The recovery mode is `bootstrap` (the default) or `signed-request`. Set it
  with `--recovery-mode` or `DUSTPAN_RECOVERY_MODE`. When the mode is `none`,
  the installation has no break-glass: the command refuses, and the operator
  restores from a backup. To use the command, set the mode to `bootstrap`.
- `DUSTPAN_DISABLE_HOST_RECOVERY` does not turn the command off. See
  [Turn the command off](#turn-the-command-off).

## Make the temporary administrator

Pass the name of the person and the reason for the recovery. The name must
name no principal that exists.

```sh
opendustpan host create-temp-admin --database sqlite:/var/lib/dustpan/dustpan.db \
  --reason "The last site administrator left the company" alice
```

With no `--reason`, the command asks for the reason on a terminal. With no
terminal and no `--reason`, it refuses.

With SQLite, stop the server first. With PostgreSQL, the server can keep running.

The command prints the person, the secret of a new personal access token,
and its fixed expiry:

```text
person: alice
token: <secret>
expires: 2026-10-09T12:00:00Z
```

The expiry is at most 24 hours after the create, or the maximum lifetime of
the token policy when that is shorter.

The command reads no token from a configuration file.
It prints the secret once: store it in the secret store of the person.
The person signs in with the console token scheme:

```sh
curl -H "Authorization: token <secret>" https://dustpan.acme.internal/api/user
```

The answer names the person with `temporaryAdministrator: true` and the
fixed expiry in `expiresAt`, so the console shows a banner for it.
The grant makes the person a site administrator until the expiry.
The access limits and the gates of a site administrator apply to it.
A temporary administrator cannot sign in through the identity provider: it
uses only its token.

A name that a principal already holds is refused: run the command again
with a name that no principal holds.

## What the command writes

The command makes the person, its site-administrator grant, its record and
its token in one transaction. A failure leaves none of them. The create
writes four audit entries in the scope `@server`, with the subject `@host`:

| Action | Object |
| --- | --- |
| `server:manage-principals` | the new person |
| `server:manage-access` | the site-administrator grant on the installation |
| `server:manage-temporary-administrators` | the record, with the reason |
| `server:manage-tokens` | the token of the person |

## What the command may do

A person normally makes only its own tokens, after a sign-in. Recovery has
no sign-in, so the host command has one narrow exception: it issues the
first token of the temporary administrator that the same run makes.
The token ends at the fixed expiry of the record.
The command never issues a second token or a token for another person,
and no API route reaches this exception.

## Turn the command off

Set `DUSTPAN_DISABLE_HOST_RECOVERY=true` (or `1`) in the environment of the
operator account on the host. The command then refuses to run and says so.
`false`, `0` and an empty value keep the command on. The command refuses
any other value, so a typo never turns it on. Unset the variable to use the
command again.

## What the expiry does

At the fixed expiry the server disables the principal, ends its
site-administrator grant and ends its host-recovery token, and keeps the
record, so a later use is still denied.
Each step writes an audit entry in the scope `@server` with the action
`server:manage-temporary-administrators`: the create, each sign-in, and
the expiry.

The name of an expired temporary administrator stays taken: the principal
stays, disabled. To use the name again, a site administrator deletes the
principal with `DELETE /api/server/principals/{id}`. The delete also deletes
its record.
