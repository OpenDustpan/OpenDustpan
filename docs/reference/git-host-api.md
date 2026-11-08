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

A `PUT` of the GitHub App registration with `If-Match`, when no registration
exists, answers 404. The answer is not 412. A `PUT` with `If-Match` whose version
does not match an existing registration answers 412. A `PUT` with `If-Match`
whose version matches the current version answers 200.

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
admin has the actions of a namespace. The role maintainer has
`namespace:read-git-host-connections` and `namespace:use-git-host-connection`.

The action `namespace:use-git-host-connection` lets a person name a connection
in the settings of a unit, in addition to the action on the unit. It lets the
person point a unit at the credentials of the connection. See the
[unit settings API](unit-settings-api.md).

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
  "capabilities": ["files", "compare"],
  "missing": {"write-back": ["commit"], "self-service": []}
}
```

`missing` has one list for each of the features `write-back`,
`write-back-review`, `fetch`, `runner-git`, `self-service` and `cause-diff`. The list
of a feature that the connection can serve is empty.

For an app, `scopes` lists the permissions of the installation, for example
`contents:write`, `metadata:read`, `pull_requests:write`, `statuses:write` and
`issues:write`. The capabilities `files`, `compare` and `tree` need
`contents:read`. The capability `commit` needs `contents:write`. The capability
`review` needs `contents:write` and `pull_requests:write`. The capability
`status` needs `statuses:write`: the app permission "Commit statuses" with the
access "Read and write". The capability `comment` needs `issues:write`: the app
permission "Issues" with the access "Read and write". GitHub keeps the comments
of a pull request in the issues. The server reads up to 1000 comments of a pull
request to find its own comment, so a pull request with more comments cannot get
a comment of the server. A connection with no push access has no
`commit`, no `review`, no `status` and no `comment`. See the guide "Connect
GitHub" for the permissions of the app.

## Errors

| Status | Cause |
| --- | --- |
| 400 | A body or a value that is not valid, or an address that the outbound rules refuse |
| 400 | `If-Match` is not one version in quotes. The error code is `request.if-match-invalid` |
| 404 | An object that does not exist, or no registration |
| 409 | A conflict. Examples: a second connection to one installation, the default connection, a registration in use, no registration for a new connection. Also the installation that the operator did not assign to the namespace, for create and for check. Also a check when the server cannot open the stored key of the app. Also a check when the address rules refuse the address of the app |
| 412 | `If-Match` does not match the version |
| 413 | The body is larger than 64 KiB |
| 422 | GitHub refused the credential |
| 429 | `check` came more than once in 10 seconds for one connection, or more than 30 times in a minute for one installation. `Retry-After` gives the wait time, at most 60 seconds |
| 503 | GitHub did not answer. `Retry-After` gives the wait time when GitHub gave one, at most 60 seconds |
