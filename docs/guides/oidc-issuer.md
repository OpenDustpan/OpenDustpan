# OIDC issuer

Each namespace of OpenDustpan is an OIDC issuer. A cloud or another system that
trusts the issuer of a namespace lets a job of that namespace in without a
stored secret. The cloud holds the issuer URL, and the server proves who it is
with a short-lived token that it signs.

This page shows how to set up the issuer, how the keys change, and what to do
when the keys leak. The routes are in the [issuer API](../reference/issuer-api.md).

## Set the issuer host

The issuer URL of a namespace is `<issuer host>/oidc/<namespace ID>`, for
example `https://issuer.example.com/oidc/ns-k3v9q2m7x4t8p1w6z5r0`. The path has
the stable ID of the namespace. A new name of the namespace keeps the issuer. A
namespace that you delete and make again with the same name gets a new ID, so
it never gets the cloud trust of the old namespace.

Set the issuer host with `--issuer-base-url` (`DUSTPAN_ISSUER_BASE_URL`). When
you set none, the server uses `--public-url`. A server with neither setting
signs no token, and the discovery routes answer 404.

The cloud stores the issuer URL. Choose a host that stays the same:

- A change of `--issuer-base-url`, or of `--public-url` when you set no issuer
  base URL, breaks the trust of each cloud for each namespace.
- A move of a namespace to another installation keeps the trust only when the
  new installation answers the same issuer URL.

Read the issuer of a namespace to see the URL that it uses now:

```sh
curl -H "Authorization: token $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/issuer
```

The answer holds `effectiveIssuerUrl` and `issuerHostConfigured`.

## Keys

A key is an RSA key of 2048 bits that signs with `RS256`. This is the only
algorithm of the issuer. Azure accepts only `RS256`, and the other clouds accept
it. A key has one of four states:

| State | In the JWKS | Signs |
| --- | --- | --- |
| `next` | yes | no |
| `active` | yes | yes |
| `retired` | yes | no |
| `deleted` | no | no |

The server seals each private key with the data key of the namespace before it
stores the key. Only the part of the server that signs opens a private key,
and only in the memory of the instance, for at most one minute. No route
answers a private key. The backup of the namespace holds the sealed keys. A
restore on the installation that made the backup keeps the keys, so the trust of
each cloud stays. A restore on another installation restores no key: the
namespace gets new keys, and its clouds must trust the new issuer URL. This
way two installations never sign with one key.

A namespace with no key gets an `active` key and a `next` key at its first
token, or when you call `POST /issuer/keys`. A namespace that never opens a
login block has no key, and its JWKS is empty.

### Rotation

The schedule `issuer.rotate` runs each day:

- An `active` key signs for 90 days.
- At the end, the `next` key becomes `active` and the old key becomes
  `retired`.
- Seven days before the end, the schedule makes a new `next` key if there is
  none. The notice `issuer.key-next` names the time when it becomes active.
  Add the new key to the trust of a relying party that needs a fixed key, for
  example Keycloak, before that time.
- A `retired` key stays in the JWKS for 7 days, and then it is `deleted`.

The server puts a new key in the JWKS before the first token that it signs.
A cloud reads the JWKS again after some minutes or hours, so a new key must be
there first. A token that the old key signed stays valid until it ends, at most
60 minutes later.

To rotate now, call `POST /issuer/keys/rotate`. This is a normal rotation.

### Emergency rotation

If you think that a key leaked, for example a copy of the database or of the
key-encryption key, rotate in an emergency:

```sh
curl -X POST -H "Authorization: token $TOKEN" \
  -d '{"emergency": true, "reason": "database copy found on a laptop"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/issuer/keys/rotate
```

The server deletes every earlier key at once, also the `next` key, because the
same leak may have exposed it. It makes a new `active` key and a new `next` key.
It sends the notice `issuer.key-revoked`.

An emergency rotation does not end a cloud session that a token already got. A
cloud drops the old key when it reads the JWKS again, which can take minutes
to hours. A session that the cloud already made, for example an AWS STS
session of up to 12 hours, stays valid until it ends. Revoke these sessions
at the cloud.

## The tokens

A token lives 5 minutes by default. The longest life is 60 minutes. `aud` is one
string. The server refuses an audience of the public URL of the installation
and each URL under it, so a token of the issuer never works as a credential for
OpenDustpan. An audience has 1 to 255 bytes and no control character.

The server writes an audit entry for each token. The entry holds the profile, the
block, the audience, the subject, the `jti`, the expiry and the `kid`. It
never holds the token. Use the `jti` to find the cloud sessions of an incident.

## Host the issuer files yourself

A server on a private network is often not reachable from the internet, but a
cloud such as AWS or Azure must read the JWKS. For this case a namespace can set
`issuerUrl` to an `https` URL that it hosts, for example a bucket of static
files. Then:

- Each token has that URL as `iss`.
- The server signs only with a key that a copy of the JWKS holds.

Do these steps:

1. Make the keys: `POST /issuer/keys`.
2. Set `issuerUrl` with `PATCH /issuer`. This change needs a console session
   with a recent sign-in.
3. Export the files: `GET /issuer/export`. Save the file with the path
   `.well-known/openid-configuration` and the file with the path `jwks` under
   the `issuerUrl`.
4. Check the copy: `POST /issuer/keys/check-copy`. The server reads the two files
   at the `issuerUrl`. The check passes only when each step passes.

Copy the files again after each rotation. The export holds the keys of the
states `next`, `active` and `retired`, the same keys as the route
`/oidc/{nsid}/jwks`. A token that a key signed just before a promotion still
verifies after the next copy.

The server never signs with a key that no copy holds:

- A scheduled promotion and a normal rotation run the copy check first. If the
  server does not find the `next` key in the copy, the rotation changes
  nothing. A normal rotation answers 409 with `issuer.key-missing`.
- After an emergency rotation, the new `active` key is in no copy. Each token
  fails with `issuer.key-missing` until you copy the new files and the check
  finds the key.
- The schedule runs the check each day. When it fails, the active key keeps
  signing after its 90 days. The server sends the notice
  `issuer.key-copy-missing` each day, and `GET /issuer` shows a warning with
  the days that remain.
- An active key never signs for more than 180 days. After that, a token fails
  with `issuer.key-expired` until you copy the files and the check passes.

Google Cloud, Vault and Kubernetes can take the keys with no URL.

## Notices

| Notice | When |
| --- | --- |
| `issuer.key-next` | The namespace has a new `next` key. It names the time when the key becomes active. |
| `issuer.key-revoked` | An emergency rotation ran. |
| `issuer.key-copy-missing` | The copy check at the `issuerUrl` failed. |

A notice holds the namespace, the key ID and the times. It never holds a key. A
server sends a notice only when it has a notification outbox.
