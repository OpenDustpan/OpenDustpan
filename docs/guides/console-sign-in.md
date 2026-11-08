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

## Set providers through the API

An administrator can also add sign-in providers through the API, with no
restart. The server keeps them in its database. Each provider has its own
ID, issuer and client, so several providers can work side by side.

```sh
curl -X POST https://dustpan.example/api/server/sign-in-providers \
  -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  -d '{"id":"partner","issuer":"https://idp.partner.example","displayName":"Partner",
       "clientID":"dustpan","clientSecret":"...","scopes":["profile","email"]}'
```

| Route | Action |
| --- | --- |
| `GET /api/server/sign-in-providers` | `server:read-tokens` |
| `GET /api/server/sign-in-providers/{id}` | `server:read-tokens` |
| `POST /api/server/sign-in-providers` | `server:manage-sign-in` |
| `PATCH /api/server/sign-in-providers/{id}` | `server:manage-sign-in` |
| `DELETE /api/server/sign-in-providers/{id}` | `server:manage-sign-in` |

The rules:

- The server needs the public URL with HTTPS, and a key-encryption key
  (`DUSTPAN_KEY_ENCRYPTION_KEY`). It encrypts the client secret with that key.
  No answer and no audit entry holds the client secret. An answer has
  `clientSecretSet` only.
- The list also holds the providers of the flags, with the source
  `static`. The API cannot change them: change the flags instead.
- A `PATCH` sends `version`, the version that it read. When the provider
  has another version, the change answers 409. Read the provider again
  and send the change again.
- A change applies at the next sign-in, with no restart.
- The issuer must be an `https` URL. The server accepts an `http` URL only
  on a loopback host, for tests.
- The server does not read the discovery document of a new provider. The
  first sign-in through a provider with a bad issuer answers 502.
- At a sign-in, the server reads the discovery document from the issuer.
  So only a site administrator may set the issuer: give
  `server:manage-sign-in` to no other person.
- Disable a provider with `"enabled": false`. A disabled provider answers
  404 at the start of a sign-in. The disable also ends every active
  session of a person with a subject of the provider, and its audit entry
  holds the count as `sessionsEnded`. A sign-in through the provider that
  is in progress gets no session. The server deletes a provider only
  when no principal has a subject of its ID. Else the delete answers 409.
  So a provider that the server deletes has no session.
- A change from a console session needs a sign-in inside the token
  freshness window, as a new token does.

## Plain OAuth 2.0 providers

A provider with no OpenID Connect, for example GitHub, signs persons in
through plain OAuth 2.0. Such a provider has no discovery document and no
ID token. So the server calls its user-info endpoint with the access
token, and a claim of the answer identifies the person.

Create such a provider through the API with `"mode": "oauth"`. It needs
the authorization URL, the token URL and the user-info URL, and no
issuer:

```sh
curl -X POST https://dustpan.example/api/server/sign-in-providers \
  -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  -d '{"id":"github","mode":"oauth","displayName":"GitHub",
       "clientID":"...","clientSecret":"...","scopes":["read:user"],
       "authorizationURL":"https://github.com/login/oauth/authorize",
       "tokenURL":"https://github.com/login/oauth/access_token",
       "userinfoURL":"https://api.github.com/user",
       "claims":{"subject":"id","name":"name","username":"login","email":"email"},
       "autoCreate":false}'
```

The rules:

- Each URL must be an `https` URL. The server accepts an `http` URL only
  on a loopback host, for tests.
- `claims.subject` names the claim that identifies the person. Its value
  must be a string or a whole number, and it must never change for a
  person. Use an ID, not a name or an email address. The defaults are
  `sub`, `name`, `preferred_username` and `email`.
- The subject of a person is `<provider ID>:<value of the subject claim>`.
- `autoCreate` is false when absent: any account at such a provider, for
  example any GitHub user, can complete a sign-in. So an administrator
  makes the principal of each person first, with that subject. Set
  `"autoCreate": true` only for a provider whose accounts are all your
  people.
- The server records the user-info URL and the subject claim of each
  provider ID. A change of either one answers 409, as a change of the
  issuer of an OpenID provider does. A change of the mode answers 400.
  Make a new provider ID for another identity provider.
- The server sends a PKCE challenge and a state, as for an OpenID
  provider.

### A fresh sign-in through a plain OAuth 2.0 provider

A plain OAuth 2.0 provider has no `max_age` and no `auth_time`. So the
server cannot make the provider ask the person for a password or a second
factor again. The server also cannot read when the person last gave them.
The server takes the time of the sign-in as the time of the
authentication.

This gives less protection than an OpenID provider. A new sign-in with
`fresh=1` proves only that the person has a live session at the provider.
Many providers sign the person in again from that session, with no
password and no second factor. Thus a person or a process with access to
a browser that has a live session at the provider can start a fresh
console session. Then it can create access tokens for that person.

OpenDustpan has no second factor of its own. For people who sign in
through a plain OAuth 2.0 provider, do these steps:

- Make the provider require a second factor for each of these people, for
  example in the settings of your GitHub organization.
- Give these people only the roles that they need.
- Use an OpenID provider for people with administration roles, if you
  can.

If you do not do these steps, you accept this risk: a person or a process
with access to a live session at the provider can create access tokens in
the name of that person.

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

By default, the first sign-in of a person through an OpenID provider
makes a principal with the source `oidc`, no grant and no group. A plain
OAuth 2.0 provider does so only with `"autoCreate": true`, and the
principal has the source `oauth`. The person can sign in, but can do
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

Both cookies are `Secure` and `HttpOnly`, with the path `/` and no domain.
The `__Host-dustpan_signin` cookie is `SameSite=Lax`, so the browser sends it
when the provider sends the browser back. The `__Host-dustpan_session`
cookie is `SameSite=Strict`.

### Sign in with a passkey

Local users and break-glass accounts do not use a provider. They can sign in
with a passkey and no user name. The console calls
`POST /api/dustpan/v1/sign-in/passkey/begin`, gives the options to the
browser, and sends the result to `POST /api/dustpan/v1/sign-in/passkey/finish`.
The second route sets the same `__Host-dustpan_session` cookie. The steps,
the limits and the reasons for a failure are in
[Sign in with a passkey](passkeys.md#sign-in-with-a-passkey).

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

A console on another origin sends a preflight request. For an origin in
`--allowed-origins`, the server allows the headers `Authorization`,
`Content-Type`, `Dustpan-Channel`, `Idempotency-Key`, `If-Match` and
`X-Dustpan-CSRF`, and the methods GET, POST, PUT, PATCH and DELETE, with
credentials. It caches the answer for 600 seconds. On an answer to a request,
the server sets `Access-Control-Expose-Headers: Retry-After, ETag, Idempotent-Replayed`,
so that the console can read the wait time of a 503 answer, the `ETag` of a profile and
the mark of a replayed answer. A request from another origin gets no CORS headers.

The server answers CORS on `/api/` (including the TFE-compatible API at
`/api/v2/` and the context API at `/api/context/`), and not on
`/tofu/state/`.

Each value of `--allowed-origins` has the form `scheme://host[:port]`. The
scheme is `http` or `https`. The host is in lower case and uses ASCII only
(write an internationalized host in punycode). The value has no default port
(`:443` for `https`, `:80` for `http`), no path, query or fragment, and no
trailing slash. The server refuses to start with the value `null`, the value
`*`, or any other value that does not have this form.

The APIs of the console take the session cookie: the installation API
(`/api/server/`), the tool-neutral API (`/api/dustpan/v1/`), the audit API
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
on the same routes as with a token. An access token never creates or
rotates a token: the request answers 403 with the error code
`mfa.fresh-step-required`, and changes nothing. The sign-in of the session
must be fresh: the token freshness window of the session policy sets how old it
can be. The default is 15 minutes, and 0 turns the check off.

When the sign-in is older than the window, a create or a rotation answers
401 with the field `reauthenticate`, for example
`/api/server/sign-in/corp?fresh=1`. The console sends the browser to that
path, with its own `return` parameter. With `fresh=1`, the server asks the
identity provider for a new authentication (`max_age=0`), and the callback
refuses an authentication that is older. The new session then creates the
token. For a plain OAuth 2.0 provider, see "Plain OAuth 2.0 providers":
the window then counts from the last sign-in round trip.

The same rule holds for these changes:

- the session policy and the sign-in providers
- the token policy
- the roles (`POST` and `PATCH`)
- the grants on the installation, on a namespace, on a collection and on a
  principal (`POST`)
- the members of a group (`POST` and `DELETE`) and the delete of a group,
  because a grant to a group applies to each member
- the approval of a change of the audit chain. Sign in before the server
  restarts with the new setting: maintenance mode refuses a new sign-in
- the IP allowlist of a namespace (`PUT` and `DELETE`)
- the accept of a link from a Git host account to the own principal, because
  the comment commands of that account then run as the principal
- the reveal of a secret value in the console, for a Pulumi stack, for an
  OpenTofu state version and for a profile (`showSecrets=true`)

An access token answers 403 with `mfa.fresh-step-required`. A session with an
old sign-in answers 401 with `reauthenticate` and the same error code. A
session with a sign-in time after the current time is also too old. A refused
change adds a failure entry to the audit log.

Set the window with `PATCH /api/server/session-policy` and the field
`tokenFreshnessSeconds` (action `server:manage-sign-in`): 0, or 60 to
604800 seconds. A change of the session policy from a console session
also needs a sign-in inside the window, so an old session cannot turn the
window off.

## Limits of the answer of a provider

The server checks the person that an OpenID provider or a plain OAuth 2.0
provider names before it finds the principal:

- The subject has 1 to 255 bytes and no control character.
- The name, the user name and the email each have at most 256 bytes and
  no control character.
- The `auth_time` claim of an OpenID provider is not more than 1 minute
  after the time of the server. For a sign-in with `fresh=1`, it is not
  more than 1 minute before the start of the sign-in.

The server refuses an answer that breaks a limit. It never cuts a value to
the limit. The audit entry of each refused sign-in records the real reason,
the provider ID and the method kind (`oidc` or `oauth`) in its changes with
the keys `reason`, `provider` and `method`. The reason is one of these:

- `callback does not match the sign-in request`: the state, the sign-in
  cookie or the stored sign-in request does not match, or the request was
  used before.
- `identity provider answered an error`: the provider sent an error in the
  callback, or its token endpoint refused the code.
- `ID token was refused`: an OpenID Connect provider sent an ID token that
  failed a check. A plain OAuth 2.0 provider has no ID token and never has
  this reason.
- `identity provider answer failed a check`: another answer of the provider
  failed a check, for example the user-info answer of a plain OAuth 2.0
  provider.
- `identity provider is not available`: the provider did not answer, or the
  network refused the connection.
- `sign-in provider is off`: the provider is turned off.
- `sign-in assertion is not valid`: the sign-in method accepted the answer,
  and the second check of the server refused it. The password sign-in of a
  local user has this check and this reason too.
- `sign-in input is not valid`: the callback failed the form check of the
  method.
- `sign-in value was used before`: a one-use value came a second time.
- `sign-in method failed for a reason that the service does not know`: the
  method returned an error of no known class. The sign-in fails closed.
- `no principal`, `principal is disabled`, `principal cannot sign in through
  an identity provider`, `subject is not valid` and `subject over 255
  bytes`: the answer was good, and the server refused the person. These
  refusals answer `403` with the reason.
- `assertion names another provider than the route` and `assertion method is
  not the method of the provider`: the method returned a person of another
  provider or of another kind than the provider of the route.

The server log has the rule that failed.

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
