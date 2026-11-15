# Issuer API

Each namespace is an OIDC issuer. The issuer signs tokens that other systems
trust, for example a cloud. This page describes the routes that publish the
public keys of an issuer and the routes that read and change its settings and
keys. The guide [OIDC issuer](../guides/oidc-issuer.md) explains how an
operator uses them.

## The protocol routes

These two routes need no sign-in. They answer public data only: the discovery
document and the public keys. They are outside the versioned API, because the
path is part of the issuer URL that a cloud stores. A new version of the API
never moves them.

| Method and path | Answer |
| --- | --- |
| `GET /oidc/{nsid}/.well-known/openid-configuration` | The discovery document |
| `GET /oidc/{nsid}/jwks` | The public keys of the states `next`, `active` and `retired` |

`{nsid}` is the stable ID of the namespace. It starts with `ns-`. It does not
change when the namespace gets a new name. An ID that no namespace has
answers 404 with the error code `namespace.not-found`. A server with no
issuer host answers 404 with the error code `issuer.not-configured`. A
namespace with no key answers its discovery document and the JWKS
`{"keys":[]}`.

Both routes send `Cache-Control: max-age=300`.

The discovery document has these members:

| Member | Value |
| --- | --- |
| `issuer` | The issuer URL of the namespace |
| `jwks_uri` | The issuer URL and `/jwks` |
| `response_types_supported` | `["id_token"]` |
| `subject_types_supported` | `["public"]` |
| `id_token_signing_alg_values_supported` | `["RS256"]` |
| `scopes_supported` | `["openid"]` |
| `claims_supported` | The claims that a token can hold |

A token never holds `auth_time`, `amr` or `acr`. A relying party can read these
claims as proof that a person signed in a short time ago, or with a stronger
factor. The issuer signs for a job or a run, not for a sign-in of a person. An
open whose claims hold one of these names fails, and the server signs no token.

Each key of the JWKS has `kty` (`RSA`), `use` (`sig`), `alg` (`RS256`), `kid`,
`n` and `e`. A key ID starts with `oik-`. No answer holds a private key.

## The issuer URL

The issuer URL of a namespace is `<issuer host>/oidc/<namespace ID>`. The
issuer host is the setting `--issuer-base-url`. When it is empty, the issuer
host is `--public-url`. A server with neither setting issues no token.

A namespace can set `issuerURL` to an `https` URL that it hosts, for example a
bucket of static files. Each token of the namespace then has that URL as `iss`,
and the discovery document answers it as `issuer`. See the guide.

The issuer URL is the identity of the issuer at each cloud. A change of the
issuer host, or of the public URL when the issuer base URL is empty, breaks the
trust of each cloud for each namespace that has no `issuerURL`.

## The management routes

The paths in this section start with `/api/dustpan/v1/namespaces/{namespace}`.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /issuer` | `namespace:read` | The settings, the effective issuer URL, the metadata of each key and the last copy check |
| `PATCH /issuer` | `namespace:manage-issuer` | The new settings |
| `POST /issuer/keys` | `namespace:manage-issuer` | 201 and the first keys. 409 when the namespace has keys. |
| `POST /issuer/keys/rotate` | `namespace:manage-issuer` | The keys after the rotation |
| `POST /issuer/keys/check-copy` | `namespace:manage-issuer` | The outcome of each step of the copy check |
| `GET /issuer/export` | `namespace:manage-issuer` | The two files for the `issuerURL` |

The role `admin` holds `namespace:manage-issuer`.

### Read

The answer has these members: `settings`, `effectiveIssuerURL`,
`issuerHostConfigured`, `notices`, `keys`, `copyCheck` and `warnings`.
`notices` is `on` when the server has a notification outbox, and `off` when it
has none and only logs each notice that it would send. The answer has the
header `ETag`. `settings` shows the effective value of each setting, also when
nobody changed it:

| Setting | Default |
| --- | --- |
| `issuerURL` | Empty: the issuer URL is `<issuer host>/oidc/<namespace ID>` |
| `subjectTemplate` | `namespace`, `collection`, `tool`, `unit`, `access` |
| `allowBlockSubjects` | `true` |
| `includeActorName` | `false` |

Each key in `keys` has `id`, `state`, `algorithm` and the times `createdAt`,
`activatedAt`, `retiredAt` and `deletedAt` (RFC 3339, only when they apply).
The active key has `expiresAt`: the time when the schedule rotates it. The
answer lists the deleted keys too, so an audit can see the history. It holds
no key material.

`warnings` is not empty when the namespace has an `issuerURL` and its active
key is older than 90 days. A text names the days that remain before the key
stops signing, and the routes to call.

### Change the settings

`PATCH /issuer` takes a JSON Merge Patch. Send `Content-Type:
application/merge-patch+json` and `If-Match` with the `ETag` that you read.
A request with no `If-Match` answers 428. A tag that is not the current tag
answers 412. The body names the settings to change: `issuerURL`,
`subjectTemplate`, `allowBlockSubjects` and `includeActorName`. A member set to
`null` restores its default. The answer has the new settings and the new
`ETag`.

`issuerURL` is empty or an absolute `https` URL with a host, with no user
information, query, fragment or trailing slash. A subject template names keys
from the list of subject keys and has `namespace` or `namespace_id`. The server
stores the keys in the order of the list. A value that fails these rules
answers 400 with the error code `issuer.invalid`.

A change that reduces security needs a console session with a recent sign-in:

- any change of `issuerURL`,
- any change of `subjectTemplate`,
- `allowBlockSubjects` from `false` to `true`,
- `includeActorName` from `false` to `true`.

An access token answers 403 with the error code `mfa.fresh-step-required`.
A session with an old sign-in answers 401 with the same error code and the
field `reauthenticate`. Each change writes an audit entry that lists the old
and the new value of each setting that it changed.

### Make and rotate the keys

`POST /issuer/keys` makes an `active` key and a `next` key for a namespace that
has none. The server also makes them at the first token of a namespace that has
no `issuerURL`. A namespace with an `issuerURL` and no key never gets a key
from a token: the server needs a key that a copied file holds first.

`POST /issuer/keys/rotate` rotates the keys now. The body is optional:

| Member | Meaning |
| --- | --- |
| `emergency` | `true` for an emergency rotation |
| `reason` | Why. An emergency rotation needs it. At most 500 characters. |

A normal rotation makes the `next` key active, retires the old key, and makes a
new `next` key. When the namespace has no `next` key, the rotation makes one
and promotes nothing: a relying party must see a key in the JWKS before the key
signs. A `next` key becomes active only after it was in the JWKS for 2
hours. Some verifiers keep a JWKS for up to 60 minutes, whatever the max-age
header says. A normal rotation before that
answers 409 with the code `issuer.conflict`, and the text names the time when
the key can become active. An emergency rotation does not wait: it deletes
every earlier key at once, also the `next` key, and makes a new `active` key
and a new `next` key. Its audit entry has the change `minNextKeyAge`, which
says that the rotation skipped the wait and why. The answer has
`keys`, `deleted` (the IDs of the keys that the rotation deleted) and
`promoted`.

The JWKS holds at most four keys: one `active`, one `next` and two `retired`.
When a rotation would make a third retired key, it deletes the oldest retired
key at once. A token that this key signed is at most 60 minutes old.

### Copy check and export

These routes serve a namespace that has an `issuerURL`. A namespace with none
answers 400 with the error code `issuer.invalid`.

`GET /issuer/export` answers the two files that a person copies to the
`issuerURL`. Each file has `path` (relative to the `issuerURL`), `contentType`
and `content` (the exact text to save). The export reads only. It never makes
a key. For a namespace with no key it answers 409 with the error code
`issuer.key-missing`.

`POST /issuer/keys/check-copy` runs the copy check at once. The server reads
`<issuerURL>/.well-known/openid-configuration` and checks that `issuer` is the
`issuerURL` and that `jwks_uri` is `<issuerURL>/jwks`. It then reads that JWKS
and looks for the `kid` of the active key, and of the next key when the
namespace has one. Both reads go through the egress rules of the server: they
refuse private addresses, follow no redirect and take only `https`. The answer
has `ok` and a list of `steps`, each with `name`, `ok` and `detail`. The server
signs only with an active key that a check found in the copy.

## Errors

| Code | Status | Meaning |
| --- | --- | --- |
| `issuer.invalid` | 400 | A setting or a request is not valid |
| `issuer.conflict` | 409 | The namespace already has keys |
| `issuer.key-missing` | 409 | The namespace has no key, or no key that a copied JWKS holds |
| `issuer.key-expired` | 409 | The active key signed for 180 days with an `issuerURL` |
| `issuer.not-configured` | 404 | The server has no issuer host |
| `mfa.fresh-step-required` | 403 or 401 | The change needs a recent sign-in |

## Audit

Each change of the settings, each make and rotation of keys and each copy check
writes an audit entry with the action `namespace:manage-issuer`. The schedule
writes the same entries with the subject `@server` and a reason that starts with
`issuer.rotate:`. An entry never holds a key.

Each token that the server signs writes an audit entry of the kind `access`
in the transaction that records the open that asked for it. The entry holds the
profile, the block, the audience, the subject, the `jti`, the expiry and the
`kid`. It never holds the token. A team finds each cloud session of an incident
by the `jti` in the logs of the cloud.
