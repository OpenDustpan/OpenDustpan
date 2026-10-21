# Git host API

The Git host API holds the registration of the GitHub App of the installation,
the map of its installations to namespaces, and the connections of a namespace
to GitHub. See `docs/guides/connect-github.md` for the steps.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. Each write takes the header `Idempotency-Key`, except
the check, which refuses it. A write that changes one object takes the optional
header `If-Match` with the version of the object. A version that does not match
answers 412.

| Method and path | Action | Object | Answer |
| --- | --- | --- | --- |
| `GET /git-host-apps/github` | `server:read-git-host` | the installation | The registration, with no secret |
| `PUT /git-host-apps/github` | `server:manage-git-host` | the installation | The registration. 201 for the first, 200 for a replacement |
| `DELETE /git-host-apps/github` | `server:manage-git-host` | the installation | No body. 409 while an installation is assigned |
| `GET /git-host-installations` | `server:read-git-host` | the installation | The assignments |
| `GET /git-host-installations/{installation_id}` | `server:read-git-host` | the installation | One assignment |
| `PUT /git-host-installations/{installation_id}` | `server:manage-git-host` | the installation | The assignment. 201 for a new one, 200 for a move |
| `DELETE /git-host-installations/{installation_id}` | `server:manage-git-host` | the installation | No body |
| `GET /namespaces/{namespace}/git-host-connections` | `namespace:read-git-host-connections` | the namespace | The connections |
| `POST /namespaces/{namespace}/git-host-connections` | `namespace:manage-git-host-connections` | the namespace | A new connection |
| `GET /namespaces/{namespace}/git-host-connections/{id}` | `namespace:read-git-host-connections` | the namespace | One connection |
| `DELETE /namespaces/{namespace}/git-host-connections/{id}` | `namespace:manage-git-host-connections` | the namespace | No body |
| `POST /namespaces/{namespace}/git-host-connections/{id}/make-default` | `namespace:manage-git-host-connections` | the namespace | The connection |
| `POST /namespaces/{namespace}/git-host-connections/{id}/check` | `namespace:manage-git-host-connections` | the namespace | The account, the capabilities and what each feature misses |

The role installation operator has the two actions of the installation. The role
admin has the two actions of a namespace. The role maintainer has
`namespace:read-git-host-connections`.

## Documents

A registration:

```json
{
  "kind": "github", "appID": "12345", "apiAddress": "https://api.github.com",
  "hookID": "0f3a...", "version": 1,
  "privateKey": {"set": true, "setID": "set-...", "createdAt": "2026-10-21T09:30:00Z"},
  "eventSecret": {"set": true, "setID": "set-...", "createdAt": "2026-10-21T09:30:00Z"},
  "createdAt": "2026-10-21T09:30:00Z", "updatedAt": "2026-10-21T09:30:00Z"
}
```

The body of `PUT` holds `appID`, `apiAddress`, `privateKey` and `eventSecret`.
All four are required. `privateKey` and `eventSecret` are write-only: no answer
and no audit entry holds them.

A connection:

```json
{
  "id": "gitconn-...", "namespace": "acme", "kind": "github", "auth": "app",
  "appScope": "installation", "installationID": "67890", "appID": "12345",
  "apiAddress": "https://api.github.com", "hookID": "0f3a...",
  "credentialSets": [], "push": false, "default": true, "version": 1,
  "createdAt": "2026-10-21T09:30:00Z", "updatedAt": "2026-10-21T09:30:00Z"
}
```

The body of `POST` holds `kind` (`github`), `auth` (`app`), `installationID` and
`push`. A body with `apiAddress`, `appID`, `privateKey`, `eventSecret`,
`credential` or `credentials` answers 400.

A check:

```json
{
  "account": {"id": "99", "login": "acme-org", "expiresAt": null, "noExpiry": false,
              "scopes": ["contents:read", "metadata:read"]},
  "capabilities": [],
  "missing": {"write-back": ["files", "compare", "commit"], "catalog": ["files"]}
}
```

`missing` has one list for each of the features `write-back`,
`write-back-review`, `fetch`, `runner-git`, `catalog` and `cause-diff`.

## Errors

| Status | Cause |
| --- | --- |
| 400 | A body or a value that is not valid, or an address that the outbound rules refuse |
| 404 | An object that does not exist, or no registration |
| 409 | A conflict. Examples: a second connection to one installation, the default connection, a registration in use, no registration for a new connection, an installation of another namespace |
| 412 | `If-Match` does not match the version |
| 422 | GitHub refused the credential |
| 503 | GitHub did not answer. `Retry-After` gives the wait time when GitHub gave one |
