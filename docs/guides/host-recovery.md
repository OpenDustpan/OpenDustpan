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

## Make the temporary administrator

Pass the name of the person. The name must name no principal that exists.

```sh
opendustpan host create-temp-admin --database sqlite:/var/lib/dustpan/dustpan.db alice
```

With SQLite, stop the server first. With PostgreSQL, the server can keep running.

The command prints the person, the secret of a new personal access token,
and its fixed expiry, 24 hours after the create:

```text
person: alice
token: <secret>
expires: 2026-10-09T12:00:00Z
```

The command reads no token from a configuration file.
It prints the secret once: store it in the secret store of the person.
The person signs in with the console token scheme:

```sh
curl -H "Authorization: token <secret>" https://dustpan.acme.internal/api/user
```

The answer names the person with `temporaryAdministrator: true` and the
fixed expiry in `expiresAt`, so the console shows a banner for it.
The grant makes the person a site administrator until the expiry.

A name that a principal already holds is refused: run the command again
with a name that no principal holds.

## What the command may do

A person normally makes only its own tokens, after a sign-in. Recovery has
no sign-in, so the host command has one narrow exception: it issues the
first token of the temporary administrator that the same run just made.
The token ends at the fixed expiry, at most 24 hours after the create.
The command never issues a second token or a token for another person,
and no API route reaches this exception.
The create of the person and of the token each write an audit entry.

## Turn the command off

Set `DUSTPAN_DISABLE_HOST_RECOVERY=true` in the environment of the
operator account on the host. The command then refuses to run and says
so. Unset the variable to use the command again.

## What the expiry does

At the fixed expiry the server disables the principal, ends its
site-administrator grant and ends its host-recovery token, and keeps the
record, so a later use is still denied.
Each step writes an audit entry in the scope `@server` with the action
`server:manage-temporary-administrators`: the create, each sign-in, and
the expiry.
