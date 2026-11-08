# Principals and groups

This guide shows how to create, read, change, disable and delete principals,
and how to make groups and manage their members.

A principal is a party that can hold a grant. A principal is a person, a
service or a workload. A group holds principals. A grant that names a group
gives each member of the group the rights of the grant. See
[Roles and grants](roles-and-grants.md) for the grants.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`. The
caller needs the action that each step names. Each route needs a sign-in, see
[Access tokens](access-tokens.md) for how to make a token.

A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. The group changes below need a fresh sign-in, and each one names the console path.

## Principal kinds

| Kind | What it is |
| --- | --- |
| `person` | A person who signs in to the console or the CLI. Only a person can be a break-glass account. |
| `service` | A machine that uses a service token. |
| `workload` | A machine that proves its identity with a JWT or a client certificate. |

## Create a principal

`POST /api/server/principals` creates an installation principal. The caller
needs `server:manage-principals` on the installation. The answer is `201` with
the new principal.

| Field | Required | Rule |
| --- | --- | --- |
| `subject` | yes | 1 to 255 characters. The first character is a letter or a digit. The other characters are letters, digits, `.`, `_`, `@`, `+`, `:`, `/` or `-`. The subject is case-sensitive. |
| `kind` | yes | `person`, `service` or `workload`. |
| `displayName` | no | At most 255 bytes. Without a value, the subject is the name that people see. |

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subject":"alex@example.com","kind":"person","displayName":"Alex"}' \
  https://dustpan.example.com/api/server/principals
```

The server refuses some subjects, and it answers `400` with the code
`principal.reserved-subject`:

- a subject that starts with `dev:`,
- a subject that starts with `system:namespace-service:`,
- the bootstrap administrator subject `admin`, and a subject that starts with
  `admin` followed by a character that is not a letter.

A subject that another principal already uses gets `409` with the code
`principal.already-exists`.

The subject cannot change after the create. To change the name that people
see, change `displayName`.

### Create a namespace principal

A namespace principal belongs to one namespace. It is always a `service`
principal.

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subject":"deploy-bot","displayName":"Deploy bot"}' \
  https://dustpan.example.com/api/server/namespaces/acme/principals
```

The caller needs `namespace:manage-principals` on the namespace. The other
namespace routes for these principals are the list, the read, the change and
the delete, with the same path under `/api/server/namespaces/{namespace}/principals`.
The read routes need `namespace:read-access`. A principal of another namespace
gets `404` on these paths.

## Read principals

`GET /api/server/principals` lists the installation principals. The caller
needs `server:read-principals`. The list takes these query values:

| Query | Meaning |
| --- | --- |
| `limit` | Page size, from 1 to 1000. The default is 100. |
| `cursor` | The `nextCursor` of the page before. |
| `prefix` | Only the principals whose subject starts with this text. |
| `kind` | Only the principals of this kind. An unknown kind gets `400`. |

A page holds `principals`, `nextCursor` and `total`. An empty `nextCursor` marks
the last page. A `limit` that is not a number, or is less than 1, gets `400`
with the code `request.page-size-invalid`. A `limit` above 1000 gets `400` with
the code `principal.invalid`.

`GET /api/server/principals/{id}` returns one principal. An unknown principal
gets `404` with the code `principal.not-found`.

A principal has these fields:

| Field | Meaning |
| --- | --- |
| `id` | The ID, with the prefix `prn-`. |
| `subject` | The subject. |
| `displayName` | The name that people see. |
| `kind` | `person`, `service` or `workload`. |
| `source` | Where the principal came from, for example `local`. |
| `createdAt`, `updatedAt` | The times, as RFC 3339 strings in UTC. |
| `disabledAt` | The time of the disable, or `null` when the principal is enabled. |
| `namespace` | The namespace of a namespace principal. Empty for an installation principal. |
| `groupCount` | The number of groups the principal is in. |
| `temporaryAdministrator` | Whether the principal is a temporary administrator. |
| `temporaryExpiresAt` | The end of the temporary administrator period. |

## Change a principal

`PATCH /api/server/principals/{id}` changes `displayName` only. The body must
hold a `displayName`. An empty body gets `400`. The caller needs
`server:manage-principals`.

```sh
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"displayName":"Alex A."}' \
  https://dustpan.example.com/api/server/principals/prn-...
```

An empty `displayName` in a change sets the name back to the subject.

## Disable and enable

A disabled principal cannot sign in and cannot use its tokens. Its grants stay
in place, but they do not work until the principal is enabled again.

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/server/principals/prn-.../disable

curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/server/principals/prn-.../enable
```

Both routes need `server:manage-principals` and answer `200` with the principal.
Disabling works during maintenance mode.

| Case | Status | Code |
| --- | --- | --- |
| Disable a principal that is already disabled | `409` | `principal.disabled` |
| Enable a principal that is not disabled | `409` | `principal.not-disabled` |
| Disable the last enabled break-glass account | `409` | `principal.last-break-glass` |

An enable restores the grants of the principal. The caller must therefore hold
every action that those grants give. A caller that lacks one gets `403`.

## Delete a principal

`DELETE /api/server/principals/{id}` answers `204`. The server ends the
principal, ends its group memberships and ends its grants in one transaction.
The rows stay for the history of the access review.

The delete is refused in these cases:

| Case | Status | Code |
| --- | --- | --- |
| The last enabled break-glass account | `409` | `principal.last-break-glass` |
| The last site administrator | `409` | `access.last-site-administrator` |
| A namespace service subject | `400` | `principal.reserved-subject` |

## Groups

A group has a `name` and an optional `description`.

| Field | Rule |
| --- | --- |
| `name` | Required. 1 to 100 characters. Letters, digits, `-`, `_` and `.` are allowed. The name cannot start with `.` or `-`. Names are unique without regard to case. |
| `description` | Optional. At most 1000 bytes. |

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"payments-operators","description":"Runs the payments stacks"}' \
  https://dustpan.example.com/api/server/groups
```

The create needs `server:manage-groups` and answers `201`. A second group with
the same name, without regard to case, gets `409` with the code
`principal.already-exists`.

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/server/groups` | `server:read-principals` | `200`, a page ordered by name |
| `GET /api/server/groups/{id}` | `server:read-principals` | `200` |
| `PATCH /api/server/groups/{id}` | `server:manage-groups` | `200`. The body holds `name`, `description`, or both. |
| `DELETE /api/server/groups/{id}` | `server:manage-groups`, with a fresh sign-in | `204` |
| `GET /api/server/principals/{id}/groups` | `server:read-principals` | `200`, the groups of a principal |

A group has `id` (with the prefix `grp-`), `name`, `description`, `source`,
`createdAt`, `updatedAt` and `memberCount`.

A fresh sign-in is a sign-in that the server still accepts for a sensitive
change. If the sign-in is not fresh, the answer is `403` with the code
`mfa.fresh-step-required`. Sign in again and retry. A stored access token always gets this answer, so make these changes in the console.

## Manage group members

A member is a principal. The routes are these:

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/server/groups/{id}/members` | `server:read-principals` | `200`, a page ordered by subject |
| `POST /api/server/groups/{id}/members` | `server:manage-groups`, with a fresh sign-in | `201` with the member |
| `DELETE /api/server/groups/{id}/members/{principal}` | `server:manage-groups`, with a fresh sign-in | `204` |

Add a member:

Add a member in the console. The add needs a fresh sign-in. The body holds the principal ID:

```json
{"principalID":"prn-..."}
```


| Case | Status | Code |
| --- | --- | --- |
| The principal is already a member | `409` | `principal.already-exists` |
| The group or the principal does not exist | `404` | `principal.not-found` |
| The group is managed by a directory | `409` | `principal.managed-by-directory` |

A group that a directory manages does not take membership changes from this
API. Change the membership in the directory.

## The mass removal alert

The server counts the memberships that one source ends or suspends in a
rolling window. The count is for each source and for each group.

The server alerts the administrators when one source crosses a limit in the
window:

- the number of memberships that it ended or suspended:
  `principal.removalAlertCount`, 1 to 10000, default 10,
- the percent of the members of one group that it ended or suspended:
  `principal.removalAlertPercent`, 1 to 100, default 20.

`principal.removalAlertWindow` holds the window (1 minute to 24 hours, default
15 minutes), as a duration, for example `15m`. The three settings are
installation settings, see [The settings
API](../reference/settings-api.md) for how to read and change them.

A larger count or percent, and a shorter window, reduce security. Such a
change needs a fresh sign-in.

The alert writes three records:

- a notification of the type `membership.mass-removal` for the administrators,
- one audit entry with the action `principal:mass-removal-alert` in the scope
  of the installation,
- one warning log line.

Each record names the source, the group, the count of members, the percent and
the window. The server writes one alert for each limit crossing, not one for
each membership.

The server refuses a sign-in when it cannot store the membership changes of
its group claims. No session starts then.

## Errors on these routes

| Code | Status | Meaning |
| --- | --- | --- |
| `principal.invalid` | `400` | A kind, a subject, a page value or a group value is not valid. |
| `request.body-invalid` | `400` | The body is not one JSON object with the fields that the route accepts. |
| `request.body-too-large` | `413` | The body is larger than 16384 bytes. |
| `principal.unsupported` | `501` | The store keeps no principals. |

The request body is limited to 16384 bytes. The server refuses an unknown
field in a body.
