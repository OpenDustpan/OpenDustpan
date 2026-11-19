# Trust rules API

A workload trust rule lets a workload, for example a CI job, use a token from
the identity provider of its platform to act as a service principal of a
namespace. The rule names the issuer of the token, how the server gets the
signing keys of the issuer, the exact audience, the conditions on the claims
of the token, and the service principal. The grants of that service principal
decide what the workload can do.

The server serves the routes when it has a trust rule store. The SQLite and
PostgreSQL stores have one.

## Routes

The routes use the path `/api/dustpan/v1/namespaces/{namespace}/trust-rules`.
The caller signs in as for the other APIs, with an access token or a console
session. A caller who cannot read the namespace receives 404. A caller who can
read it, but lacks the action of the route, receives 403.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET .../trust-rules` | `namespace:read-access` | The rules of the namespace, ordered by name, then ID |
| `POST .../trust-rules` | `namespace:manage-trust-rules`, and `principal:manage-tokens` on the principal of the rule | 201 with the rule and its `Location` |
| `GET .../trust-rules/{rule_id}` | `namespace:read-access` | The rule |
| `PATCH .../trust-rules/{rule_id}` | `namespace:manage-trust-rules` | 200 with the rule |
| `DELETE .../trust-rules/{rule_id}` | `namespace:manage-trust-rules` | 204 |

A rule lets a workload act as its principal. So a create needs the same right
as a service token of that principal: `principal:manage-tokens` on it. The
server refuses a create with an access token with 403 and
`trust-rule.sign-in-required`. It refuses a create from a request with no
console session, for example one with the bootstrap admin token, with 403 and
`mfa.fresh-step-required`. Only a person who signed in to the console, inside
the sign-in window, can create a rule.

To change a rule, update it (see "Update a rule"). A delete does not end the access tokens that a workload already received
through the rule. Revoke them with the routes of the service tokens of the
principal.

Each create and each delete writes one audit entry in the namespace, with the
action `trust-rule:create` or `trust-rule:delete` and the ID of the rule. The
entry of a create has a change for the name, the issuer, the audience, the key
source, the principal and the conditions.

## The rule

| Field | Content |
| --- | --- |
| `id` | The ID of the rule, `trr-` and 32 hex digits. The server sets it. |
| `namespace` | The namespace of the rule. The server sets it from the path. |
| `name` | 1 to 100 bytes, unique in the namespace. |
| `issuer` | The issuer. A token matches only when its `iss` claim is exactly this value. |
| `audience` | 1 to 255 bytes. A token matches only when its `aud` claim is exactly this value, or a list that holds exactly this value. |
| `keySource` | `discovery` or `static`. |
| `staticKeys` | For `static`, the JWKS of the issuer as JSON text, at most 64 KiB. Empty for `discovery`. |
| `principal` | The ID of a service principal of the namespace. |
| `conditions` | 1 to 16 conditions on the claims of the token. |
| `createdAt` | The time of the create, in RFC 3339. |
| `createdBy` | The subject of the principal that made the rule. |

With the key source `discovery`, the issuer must be an `https` URL with a host
and no user, query or fragment. The server reads the OpenID Connect discovery
document of the issuer and its JWKS through the outbound address rules of the
server, which refuse loopback, private, link-local and cloud metadata
addresses. Use it for an OpenID Connect provider, for example GitHub Actions
(`https://token.actions.githubusercontent.com`) or GitLab
(`https://gitlab.com`).

With the key source `static`, the rule holds the public keys, and the server
makes no outbound request. Use it for a JWT issuer with no discovery document,
for example a Kubernetes cluster that the server cannot reach. The JWKS holds
1 to 100 public keys of the types RSA (2048 to 8192 bits), EC (P-256 or P-384)
or OKP (Ed25519). The server refuses a key with private material, a symmetric
key, and an `alg` of `none` or of an HMAC algorithm. A set of more than one key
needs a `kid` on each key.

The server refuses an issuer at or under its own public URL, and each issuer
URL of its own OIDC issuer: the issuer base URL and the `issuerURL` of each
namespace. So a token that the server signs never becomes an access token of
the server. The server checks this at the create and again at each exchange,
because a namespace can set its `issuerURL` after a rule exists.

### Conditions

Each condition names one claim and 1 to 32 values:

```json
{"claim": "sub", "values": ["repo:acme/infra:ref:refs/heads/main"]}
```

A token matches a condition when the claim is a JSON string that is exactly
one of the values. The comparison is exact and case-sensitive. A value has no
wildcard: `*` matches only `*`. A claim that is missing, or that is a number, a
list or an object, matches no condition. A token matches a rule when it matches
each condition of the rule.

Each rule needs a condition on `sub`. An issuer such as GitHub Actions signs
tokens for each of its customers, so a rule without a condition on the
subject would accept the tokens of other customers. A condition cannot name
`iss`, `aud`, `exp`, `nbf`, `iat` or `jti`: the server checks those claims on
its own. A claim name has 1 to 64 ASCII letters, digits and the characters
`_`, `-`, `.` and `:`.

## Example

```text
$ curl -s -X POST -H "Authorization: Bearer $TOKEN" \
    -d '{"name":"deploy from main","issuer":"https://token.actions.githubusercontent.com",
         "audience":"https://dustpan.example.com","keySource":"discovery",
         "principal":"prn-k3v9q2m7x4t8p1w6z5r0",
         "conditions":[{"claim":"sub","values":["repo:acme/infra:ref:refs/heads/main"]}]}' \
    https://dustpan.example.com/api/dustpan/v1/namespaces/acme/trust-rules
```

## Update a rule

`PATCH .../trust-rules/{rule_id}` needs the action `namespace:manage-trust-rules`
and the header `If-Match` with the `ETag` of a read of the rule. The body holds
only the fields to change. A field that is missing keeps its value. The fields
are those of a create, with `spiffePattern` and `bundle`. The answer is 200
with the rule and its new `ETag`. A change of `principal` also needs
`principal:manage-tokens` on the new principal. Each update writes the audit
entry `trust-rule:update`.

A rule can name a `bundle`, the ID of the trust bundle of the namespace, and a
`spiffePattern` of at most 512 bytes, in the form
`spiffe://<trust-domain>/<path>`. The path can end in `/*`, which matches one
path segment. A `*` in another place matches only itself. A `bundle` that is
not the bundle of the namespace fails with `trust-rule.bundle-invalid`. A rule
with these fields makes no exchange yet: the check of the client certificate is
a later part.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 428 | `request.precondition-required` | The request has no `If-Match`. To get the `ETag`, send `GET .../trust-rules/{rule_id}`. |
| 400 | `request.if-match-invalid` | `If-Match` is not one revision in quotes. To get the `ETag`, send `GET .../trust-rules/{rule_id}`. |
| 403 | `trust-rule.principal-refused` | The update points the rule at a principal of another namespace. |
| 412 | `request.precondition-failed` | The rule changed since the read. The server keeps the stored rule. To get the new `ETag`, send `GET .../trust-rules/{rule_id}`. |

## Trust bundle

A trust bundle holds the CA certificates that a namespace trusts for client
certificates. A namespace has at most one bundle. The bundle keeps versions.
The first save makes the bundle with version 1 active. A later save adds a
version and keeps the active version. A rotation adds a version and makes it
active. The server keeps at most 5 versions. As for a rule create, a save, a
rotation and a delete need a console session with a recent sign-in. An access
token gets `trust-rule.sign-in-required`. The bootstrap admin token has no
console session and gets `mfa.fresh-step-required`.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET .../trust-bundles` | `namespace:read-access` | `{"bundles": []}`, or the bundle of the namespace |
| `POST .../trust-bundles` | `namespace:manage-trust-rules` | 201 with the bundle, its `ETag` and its `Location` |
| `GET .../trust-bundles/{bundle}` | `namespace:read-access` | The bundle and its `ETag` |
| `PATCH .../trust-bundles/{bundle}` | `namespace:manage-trust-rules` | 200 with the bundle as a new version |
| `DELETE .../trust-bundles/{bundle}/versions/{version}` | `namespace:manage-trust-rules` | 204 |

The body of a save is `{"pem": "<PEM text>"}`. The PEM text holds 1 to 10 CA
certificates, at most 64 KiB, and no certificate that is expired. A block that
is not a certificate is refused. The answer never holds the PEM text. It holds
the `id` of the bundle, the `active` version number, each version with its
`number`, `caCount`, `createdAt` and `overlapEndsAt`, and the `revision`.

A rotation is `PATCH` with `If-Match` and the body
`{"pem": "...", "overlapSeconds": 3600}`. `overlapSeconds` is 0 to 86400, and
0 when missing. The previous version stays valid until the overlap ends. A
delete of the active version answers 409. Each save, rotation and delete of a
version writes the audit entry `trust-bundle:save`, `trust-bundle:rotate` or
`trust-bundle:delete`, with the object `trust-bundle:<id>`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `trust-rule.bundle-invalid` | The PEM text or the overlap fails its rules. The message names the index of the CA and the count. The server stores nothing. Change the PEM text and send it again. |
| 403 | `trust-rule.sign-in-required` | The caller used an access token only. Sign in to the console, then send the request again. |
| 403 | `mfa.fresh-step-required` | The caller signed in too long ago, or has no console session (for example the bootstrap admin token). Sign in to the console again, then send the request again. To see the sign-in window, send `GET /api/dustpan/v1/me/preferences`. |
| 403 | `access.denied` | The caller lacks `namespace:manage-trust-rules`. To see the rules of the namespace, send `GET .../trust-rules`. |
| 404 | `trust-rule.bundle-not-found` | The namespace has no bundle with the ID. To list the bundle of the namespace, send `GET .../trust-bundles`. |
| 409 | `trust-rule.bundle-invalid` | The version is the active version. Rotate first, then delete the old version. |
| 412 | `request.precondition-failed` | The bundle changed since the read. To get the new `ETag`, send `GET .../trust-bundles/{bundle}`. |
| 428 | `request.precondition-required` | The rotation has no `If-Match`. To get the `ETag`, send `GET .../trust-bundles/{bundle}`. |

## Token exchange

`POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange` needs no
sign-in: the token of the workload is the credential. The body is:

```json
{"token": "<the JWT of the workload>", "rule": "<optional rule ID>"}
```

The server checks the token against the rules of the namespace, as
[the guide](../guides/workload-identity.md) says, and answers 201 with
`Cache-Control: no-store`:

```json
{"token": "odp_...", "id": "tok-...", "expiresAt": "2026-10-30T13:00:00Z",
 "principal": "prn-...", "rule": "trr-..."}
```

`token` is the secret of the access token: the server shows it once. `id` is
the ID of the access token. The token lives the exchange lifetime of the token
policy, one hour by default.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object with `token`, or `rule` is not a rule ID. |
| 401 | `trust-rule.no-match` | No rule of the namespace accepts the token, or the namespace does not exist. The answer never names the failed check: the audit log of the namespace does. |
| 409 | `trust-rule.ambiguous` | More than one rule accepts the token. Name the rule in `rule`. |

## Errors

Errors have the body `{"message": "...", "code": 400, "errorCode": "..."}`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object. It has an unknown field, a missing field, or data after the object. |
| 400 | `trust-rule.invalid` | A field fails its rules. The message names the field. |
| 400 | `trust-rule.issuer-refused` | The issuer is at or under the public URL of the server, or is an issuer URL of the OIDC issuer of the server. |
| 400 | `trust-rule.principal-refused` | The principal does not exist, has a delete or a disable, is not a service principal, or is a principal of the installation. |
| 403 | `trust-rule.principal-refused` | The principal is a principal of another namespace. The write names an object outside the namespace of the path. The refusal writes an audit entry. |
| 401 | | The request has no valid credential. |
| 403 | `access.denied` | The caller lacks the action of the route, or `principal:manage-tokens` on the principal. To see the rules of the namespace, send `GET .../trust-rules`. |
| 403 | `trust-rule.sign-in-required` | The caller used an access token only to change a rule or a bundle. Sign in to the console. |
| 403 | `mfa.fresh-step-required` | The caller signed in too long ago, or has no console session (for example the bootstrap admin token), to change a rule or a bundle. Sign in to the console again. To see the sign-in window, send `GET /api/dustpan/v1/me/preferences`. |
| 404 | `namespace.not-found` | The namespace does not exist, or the caller cannot read it. The answer is the same for both. |
| 404 | `trust-rule.not-found` | The namespace has no rule with the ID. |
| 409 | `trust-rule.already-exists` | The namespace has a rule with the name. |
| 409 | `trust-rule.limit` | The namespace has 100 rules. |

A refused create stores nothing. A create that the server refused because it
names the caller (a missing action or reach, a principal of another
namespace, or no fresh authentication) writes one audit entry with the caller
and the cause. A create that the server refused because its input is not
valid writes no audit entry.

## Backup

The rules are access data. A backup holds them. A restore replaces them only
when the caller holds `server:restore-access`, together with the principals.
A restore refuses a backup that holds a rule that fails the checks above, or a
rule whose principal is not a service principal of the namespace of the rule.
