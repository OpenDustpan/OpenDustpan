# Connect GitHub

This guide shows how to connect a namespace to GitHub with one GitHub App for
the whole installation. The operator registers the app one time and gives each
installation of the app to a namespace. A namespace administrator then makes a
connection and checks it.

A connection of this kind holds no key. The key and the event secret of the app
belong to the installation. A connection names only the GitHub installation
that the operator gave to its namespace.

The paths in this guide start with `/api/dustpan/v1`. The pages in
`docs/reference/git-host-api.md` list each path with its action.

## What this release does

This release can register the app, assign installations, make connections and
check them. A check shows the GitHub account of the installation and the
capabilities of the connection.

This release does not read files, write commits, open review requests, take
webhooks or give tokens to runners. The capabilities list of a check is empty
for that reason. The answer still names, for each feature, the capabilities
that the feature needs and the connection does not have. GitLab is not
supported yet.

## One app for the installation, or one for each namespace

One GitHub App has exactly one webhook address and one webhook secret for all of
its installations. So the app belongs to the whole installation of the
server, and not to one namespace.

- **One app for the installation.** This is the default and the only kind in this
  release. The operator registers it. The operator gives each GitHub
  installation of the app to one namespace.
- **One app for each namespace.** A namespace can register its own app later. Its key
  and event secret then belong to the namespace. This option is not in this
  release.

## Step 1: the operator registers the app

Make a GitHub App in GitHub. Download its private key (a `.pem` file) and note
its app ID. Make a long random event secret of at least 32 bytes:

```sh
openssl rand -hex 32
```

The caller needs the action `server:manage-git-host` on the installation. The
role installation operator has it.

```sh
curl -X PUT "$SERVER/api/dustpan/v1/git-host-apps/github" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "$(jq -n --rawfile key app.pem --arg secret "$EVENT_SECRET" \
        '{appID:"12345", apiAddress:"https://api.github.com", privateKey:$key, eventSecret:$secret}')"
```

The request needs all four fields. The server answers 201 and the first time,
and 200 for a replacement. The answer shows that the key and the event secret
are set, with a set ID and a creation time. It never shows their values. It also
shows the `hookID`.

Rules for the registration:

- The event secret has at least 32 bytes. The server does not store an empty
  secret.
- The API address uses `https`. For GitHub Enterprise Server, give the address
  of its API, for example `https://git.example.com/api/v3`.
- The server refuses an address that is not public: loopback, private,
  link-local and the metadata addresses of cloud providers. This check runs when
  you set the address and again at each call. To allow the range of a GitHub
  Enterprise Server on a private network, set `--git-host-allowed-ranges`
  (`DUSTPAN_GIT_HOST_ALLOWED_RANGES`) to its range, for example
  `10.20.0.0/16`. A range opens private-network addresses only (RFC 1918 and
  unique local). It must have at least 8 bits for IPv4 and 32 bits for IPv6.
  The server refuses loopback, link-local, metadata, unspecified and multicast
  addresses also inside a range.
- A replacement keeps the `hookID`. To change the key, the event secret, the
  app ID or the address, send all four fields again. Send `If-Match` with the
  version that you read to make sure that no one else changed it.
- A console session needs a recent sign-in for this change.

The server stores the key and the event secret sealed with a key of the
installation. A backup holds them sealed.

## Step 2: the operator gives an installation to a namespace

Install the app in a GitHub organization or account. GitHub gives the
installation an ID. Give it to a namespace:

```sh
curl -X PUT "$SERVER/api/dustpan/v1/git-host-installations/67890" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"namespace":"acme"}'
```

Only the operator can do this. A namespace administrator cannot. One
installation belongs to one namespace. To move it, send the request again with
another namespace. The connections of the old namespace stop at their next use.
To end the assignment, send `DELETE` to the same path. The map of
installations to namespaces is data of the installation. A backup of one
namespace does not hold it. A restore changes it only when the caller holds
`server:restore-access`. Any other restore keeps the current map.

Read the map with `GET /git-host-installations`. The list is in the order of the
installation IDs as numbers.

## Step 3: a namespace administrator makes a connection

The caller needs the action `namespace:manage-git-host-connections` on the
namespace. The role admin has it.

```sh
curl -X POST "$SERVER/api/dustpan/v1/namespaces/acme/git-host-connections" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"kind":"github","auth":"app","installationID":"67890","push":false}'
```

The answer has the ID of the connection, the app ID and the API address of the
registration, and the `hookID`. The first connection of a namespace is the
default connection.

The request has these rules:

- The installation must be one that the operator gave to this namespace. If
  not, the answer is 409.
- The request takes no key, no event secret, no API address and no app ID.
  These belong to the operator. A request with one of them gets 400.
- The answer is 409 when the operator has not registered the app.
- A namespace has one connection for each installation, and at most 50
  connections.
- The server checks at each use that the installation is still given to the
  namespace. If the operator moves it, the connection fails and the server writes
  an audit entry.

List the connections with `GET .../git-host-connections`. Read one with
`GET .../git-host-connections/{id}`. To make a connection the default, send
`POST .../git-host-connections/{id}/make-default`.

To delete a connection, send `DELETE .../git-host-connections/{id}`. The answer
is 409 for the default connection while the namespace has another connection.
Make another connection the default first. You can delete the only connection
of a namespace.

## Step 4: check the connection

```sh
curl -X POST "$SERVER/api/dustpan/v1/namespaces/acme/git-host-connections/$ID/check" \
  -H "Authorization: Bearer $TOKEN"
```

The server signs a short token with the key of the app, reads the installation
from GitHub, and asks GitHub for an installation token to prove that the
installation works. It asks for the smallest permission, read of the metadata,
and revokes the token after the call. It keeps no token. The answer shows:

- `account`: the ID and the login of the GitHub account, and the permissions of
  the installation as `scopes`.
- `capabilities`: what the connection can do in this release.
- `missing`: for each feature, the capabilities that the feature needs and the
  connection does not have.

Errors of a check:

| Answer | Cause |
| --- | --- |
| 409 | The operator did not give the installation to this namespace. Or the address of the app changed after its key was stored. Or the server cannot open the stored key. Or the address rules of the server refuse the address. |
| 422 | GitHub refused the credential. |
| 429 | The connection was checked more than once in 10 seconds, or the installation more than 30 times in a minute. The header `Retry-After` gives the wait time, at most 60 seconds. |
| 503 | GitHub did not answer, or limited the rate. The header `Retry-After` gives the wait time when GitHub gave one, at most 60 seconds. |

The check takes no `Idempotency-Key`. A retry is safe.

## Safety rules

- No answer holds a private key, an event secret or a token.
- An audit entry holds only that a secret is set, never its value.
- The server sends the key only to the API address of the registration. It
  seals the key together with a digest of that address, so the key opens only for
  that address. It also refuses to open a connection when the address and the
  stored digest do not match.
- Each write takes the header `Idempotency-Key`, and a retry with the same key
  and the same body answers the first answer.

## Webhooks and a federation

This release takes no webhook. The `hookID` that the registration shows is
for the next release. A GitHub App has one webhook address. When webhooks
arrive, use an address with a stable host name that every member of a
federation accepts, for example a name with DNS failover or a global load
balancer. Do not use the host name of one member. Then a promotion does not
change the address that GitHub stores. A member that receives an event for a
namespace that another member owns answers 204 and drops the event, and the
poll of each feature catches the change.

## Metric

`dustpan_git_host_connection_refused_total` counts the connections that the
server refused to open. Its label `reason` is `installation-not-assigned`,
`address-changed`, `no-registration`, `key-not-opened`, `address-refused`
or `other`. Alert when it grows: the
operator moved an installation, or the registration changed.
