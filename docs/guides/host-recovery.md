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

## What the expiry does

At the fixed expiry the server disables the principal, ends its
site-administrator grant and ends its host-recovery token, and keeps the
record, so a later use is still denied.
Each step writes an audit entry in the scope `@server` with the action
`server:manage-temporary-administrators`: the create, each sign-in, and
the expiry.
