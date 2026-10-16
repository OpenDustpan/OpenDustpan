# Console sign-in with an OpenID Connect provider

This guide shows how to let persons sign in to the console through one
OpenID Connect provider, for example an identity provider of your company.
The server checks the identity of the person at the provider. Then it
issues its own console session.

## What you need

- The public URL of the server, with HTTPS. Set it with `--public-url`
  (`DUSTPAN_PUBLIC_URL`). The server refuses the sign-in without it.
- A client of the server at the provider: a client ID and a client secret.
- The redirect URI of that client at the provider:
  `<public URL>/api/server/sign-in/<provider ID>/callback`.

The provider ID is a name that you choose: 1 to 32 lower-case letters,
digits and hyphens, starting with a letter. For example, `corp`.

## Set the provider

Set the issuer, the provider ID and the client ID with flags or variables.
Set the client secret only in a variable or in a file. A flag shows in
process listings, so the secret has no flag.

```sh
export DUSTPAN_OIDC_CLIENT_SECRET_FILE=/etc/opendustpan/oidc-client-secret
./bin/opendustpan serve \
  --public-url https://dustpan.example \
  --oidc-issuer https://idp.example \
  --oidc-provider corp \
  --oidc-display-name "Example Corp" \
  --oidc-client-id dustpan
```

Only the owner of the secret file can read it. The server refuses a file
that its group or other users can read. Run `chmod 600` on the file.

| Setting | Variable | Default |
| --- | --- | --- |
| `--oidc-issuer` | `DUSTPAN_OIDC_ISSUER` | (none: no sign-in) |
| `--oidc-provider` | `DUSTPAN_OIDC_PROVIDER` | (none) |
| `--oidc-display-name` | `DUSTPAN_OIDC_DISPLAY_NAME` | the provider ID |
| `--oidc-client-id` | `DUSTPAN_OIDC_CLIENT_ID` | (none) |
| `--oidc-scopes` | `DUSTPAN_OIDC_SCOPES` | `profile,email` |
| `--oidc-no-auto-create` | `DUSTPAN_OIDC_NO_AUTO_CREATE` | `false` |
| (no flag) | `DUSTPAN_OIDC_CLIENT_SECRET` | (none) |
| (no flag) | `DUSTPAN_OIDC_CLIENT_SECRET_FILE` | (none) |

The issuer must use HTTPS. The server reads the discovery document of the
provider at the first sign-in, not at start. So the server starts while the
provider is down. When the provider does not answer, the start of a
sign-in answers 502.

## Keep the provider ID

The server makes the subject of each person from the provider ID and the
subject at the provider: `<provider ID>:<sub>`. For example,
`corp:248289761001`. Never change the provider ID after the first sign-in.
A new ID makes new subjects, so each person gets a new principal with no
grant.

The issuer is not part of the subject. So the server records the issuer
of each provider ID at its first start. A later start that gives a
recorded provider ID another issuer stops before it serves, and the
error names the provider ID and both issuers. The server compares the
issuers with a lowercase scheme and host and with no trailing slash, so
`https://IdP.example/` and `https://idp.example` are the same issuer.

If the issuer changed by mistake, set `--oidc-issuer` back to the
recorded issuer. Set a new provider ID only when you move to another
identity provider: each person then gets a new principal with no grant.

A principal with the subject `<provider ID>:<sub>` is the principal of
that person, also when an administrator made it as a local principal.

The server never links a person by email address.

## The first sign-in of a person

By default, the first sign-in of a person makes a principal with the
source `oidc`, no grant and no group. The person can sign in, but can do
nothing until an administrator gives a role. Give the first roles with the
bootstrap admin token, through the grants API.

With `--oidc-no-auto-create`, the server refuses the sign-in of a person
who has no principal yet. It answers 403 and writes an audit entry with
the reason `no principal`. Then an administrator makes each principal
first, with the subject `<provider ID>:<sub>`.

The server always refuses a disabled principal, a break-glass account and
a service principal.

## The sign-in flow

1. The console sends the browser to
   `GET /api/server/sign-in/<provider ID>?return=<path>`. The return path
   must be a path of this server, for example `/console`.
2. The server stores the sign-in request for 10 minutes. It sets the
   `__Host-dustpan_signin` cookie and sends the browser to the provider.
3. The provider sends the browser back to the callback. The server checks
   the state against the cookie, uses the request once, and checks the ID
   token.
4. The server sets the `__Host-dustpan_session` cookie and sends the
   browser to the return path.

Both cookies are `Secure`, `HttpOnly` and `SameSite=Lax`, with the path `/`
and no domain.

## The session

- `GET /api/server/session` answers the session and its CSRF value.
- `POST /api/server/sign-out` ends the session.

The session policy sets the idle limit and the maximum length: 15 minutes
and 12 hours by default.

A request with the session cookie must send the CSRF value in the
`X-Dustpan-CSRF` header, unless its method is GET, HEAD or OPTIONS. The
server also refuses a request with the cookie from another site. An
`Origin` header must name the public URL or an origin of
`--allowed-origins`.

The APIs of the console take the session cookie: the installation API
(`/api/server/`), the inventory API (`/api/inventory/`), the audit API
(`/api/audit/`) and the API that works with the Pulumi CLI. Each API
applies the same CSRF and origin checks. The TFE-compatible API does not
take the cookie: it answers 401 to a request with the cookie and no token.
A request with an `Authorization` header uses the header and ignores the
cookie. A refused header answers 401, also when the cookie is valid.

The API that works with the Pulumi CLI answers 403 to a console session on
the routes that create and start an update, and on the encrypt and decrypt
routes of the secrets provider. The Pulumi CLI uses these routes with an
access token. A sign-out cannot stop an update, so a session cannot start
one.

## Tokens from a console session

A signed-in person can create and rotate access tokens from the console,
on the same routes as with a token. The sign-in of the session must be
fresh: the token freshness window of the session policy sets how old it
can be. The default is 15 minutes, and 0 turns the check off.

When the sign-in is older than the window, a create or a rotation answers
401 with the field `reauthenticate`, for example
`/api/server/sign-in/corp?fresh=1`. The console sends the browser to that
path, with its own `return` parameter. With `fresh=1`, the server asks the
identity provider for a new authentication (`max_age=0`), and the callback
refuses an authentication that is older. The new session then creates the
token.

Set the window with `PATCH /api/server/session-policy` and the field
`tokenFreshnessSeconds` (action `server:manage-sign-in`): 0, or 60 to
604800 seconds. A change of the session policy from a console session
also needs a sign-in inside the window, so an old session cannot turn the
window off.

## Audit entries

Each sign-in writes entries with the action `session:sign-in` in the
installation audit log: one for a new principal, and one for the new
session. A refused sign-in writes one entry with the reason, and counts in
the failure limit of its address. A sign-out writes an entry with the
action `session:sign-out`. No entry holds a code, a token, a state or a
cookie value.

An entry of a request with the session cookie has the channel `console`,
whatever channel the request declares. Its subject is the subject of the
person, and it has no token ID. In the installation audit log, the entry
also records the ID of the session under the key `sessionId`. The
`session:sign-in` entry of that session names the same ID in its object.
So a token that a console session creates leads back to its sign-in.
