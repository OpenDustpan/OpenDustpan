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
| `PATCH /api/server/groups/{id}` | `server:manage-groups` | `200`. The body holds `name`, `description`, `closedToIdentityProvider`, or a combination of them. |
| `DELETE /api/server/groups/{id}` | `server:manage-groups`, with a fresh sign-in | `204` |
| `GET /api/server/principals/{id}/groups` | `server:read-principals` | `200`, the groups of a principal |

A group has `id` (with the prefix `grp-`), `name`, `description`, `source`,
`createdAt`, `updatedAt`, `memberCount`, `closedToIdentityProvider` and
`protected`.

`protected` is `true` when the group holds a sensitive action on the
installation or on a principal of the installation through its own grants. A
sensitive action manages the installation or its access: each operator
action, each administration action and each delegation action, except the
read-only server actions: each `server:read-*` action. A grant inside one
namespace, and a grant that a member holds as a person, do not make the
group protected. A disabled principal of the installation counts: the group
holds a grant on a principal of the installation, and the server can enable
the principal again.

The server stores a new group before it checks whether the group is
protected. A new group holds no grant, so it is not protected.

These actions are sensitive:

| Action | Why it is sensitive |
| --- | --- |
| `installation:store-check` | Runs the deep check of the blob store, and writes throwaway objects in it. |
| `server:backup` | Starts a backup, and reads the data of the installation. |
| `server:delete-instance` | Deletes the version row of a server process. |
| `server:maintenance` | Changes the installation between normal operation and maintenance mode. |
| `server:manage-git-host` | Registers the Git host app of the installation, and maps its installations to namespaces. |
| `server:manage-settings` | Changes the settings of the installation. |
| `server:observe` | Reads the metrics of the installation. |
| `server:restore` | Starts a restore, and replaces the data of the installation. |
| `server:set-audit-chain` | Approves a change of the audit chain, on or off. |
| `namespace:manage-ip-allowlist` | Changes the addresses that can reach a namespace. |
| `server:create-namespace` | Creates a namespace, and gives its creator the admin role on it. |
| `server:disable-audit-chain` | Turns the audit chain off. |
| `server:manage-access` | Gives roles on the installation. |
| `server:manage-groups` | Creates groups, changes their members, and so changes who holds the grants of each group. |
| `server:manage-principals` | Creates, changes and deletes principals. |
| `server:manage-roles` | Defines the actions that a custom role gives. |
| `server:manage-service-tokens` | Makes the tokens of a service principal, and so acts as that principal with its grants. No route declares it, and a stored role can hold it. |
| `server:manage-sign-in` | Changes the sign-in settings, and so who can sign in to the installation. |
| `server:manage-token-policy` | Changes the token policy, and so how long a token works. |
| `server:manage-tokens` | Revokes the personal tokens of other persons. |
| `server:restore-access` | Lets a restore replace the access tables, and so who has access. |
| `principal:manage-tokens` | Makes the tokens of a service principal, and so acts as that principal with its grants. |

Each `server:read-*` action is not in the table:
`server:read-principals`, `server:read-access`, `server:read-tokens`,
`server:read-audit`, `server:read-settings`, `server:read-instances` and
`server:read-git-host`. A read-only action cannot raise a privilege, so a
group that holds one is not protected, and the identity provider can still
manage its members.

A read that can return secret material stays sensitive. A secret setting, for
example the database URL or a credential, shows only whether it holds a value,
never the value.

An action that no family classifies fails the check closed. A read that
cannot check the group answers `500` with the code `server.internal`. It
never answers `protected: false`.

A rename moves the members from the old name to the new name, so it can lift
an access limit that names either name. Such a rename needs a site
administrator, a caller who holds every action of the installation (the
site-administrator role on the installation). See
[Manage group members](#manage-group-members).

A fresh sign-in is a sign-in that the server still accepts for a sensitive
change. If the sign-in is not fresh, the answer is `403` with the code
`mfa.fresh-step-required`. Sign in again and retry. A stored access token always gets this answer, so make these changes in the console.

### Close a group to the identity provider

A group carries the switch `closedToIdentityProvider`. The default is `false`:
the group is open. A `true` value marks the group as closed to the identity
provider.

A caller with `server:manage-groups` changes the switch with
`PATCH /api/server/groups/{id}`. The body holds the switch as a JSON boolean:

```sh
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"closedToIdentityProvider":true}' \
  https://dustpan.example.com/api/server/groups/grp-...
```

A value of another type gets `400` with the code `request.body-invalid`. A
change of the switch writes an audit entry for the group. The reason of the
entry names the new value: `the identity provider may add members: off` for a
closed group, and `the identity provider may add members: on` for an open one.
A call that names the current value again is not an error.

The server stores the switch. SCIM and the group claims of a sign-in do not
read it yet, so the identity provider can still add a member to a closed
group.

## Manage group members

A member is a principal. The routes are these:

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/server/groups/{id}/members` | `server:read-principals` | `200`, a page ordered by subject |
| `POST /api/server/groups/{id}/members` | `server:manage-groups`, with a fresh sign-in | `201` with the member |
| `DELETE /api/server/groups/{id}/members/{principal}` | `server:manage-groups`, with a fresh sign-in | `204` |

An access limit is one forbid policy. A membership change can lift the
policy for the member, and then it needs a site administrator. The direction
of the change decides:

- An add of a member to a group that an `unless` clause names can lift the
  policy for that member.
- A delete of a member from a group that a `when` clause or the principal
  scope names can lift the policy for that member.

The delete of a group deletes all its members, so it counts as a delete of
each member. A rename moves the members from the old name to the new name,
so it can lift a policy that names either name. A rename runs the grants
check of the group too, so the caller must hold every action that the grants
of the group give. A caller who holds only
`server:manage-groups` gets `403` for a change that can lift a policy. A
change that can only add a policy, and a group that no access limit names,
keep the `server:manage-groups` check. When the server cannot read the group
or the limits, or a limit does not parse, only a site administrator can make
the change. The refusal names the limit, its scope and the group. The refusal
of a limit of a namespace that the caller cannot read does not name the
limit, but it shows that some access limit names the group.

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

A membership gives the grants of the group. A group that holds a delegation
role, such as `token-manager`, on a service principal gives each member the
right to act as that principal. The add needs a caller that holds the reach of
that principal. Otherwise the answer is `403`. See
[Access tokens](access-tokens.md) for the reach.

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
the window. The server writes one alert for each source in each window, all
groups included, or a repeat when a write stops after the notification or two
writes race. The limits count the removals of each source on its own, so
removals that are spread across providers do not add up. A marker with a time
after the removal counts as no marker, and the server writes an audit entry
with the code `principal.removal-alert-marker-invalid`.

When the alert cannot be written, the sign-in goes on, the server writes an
ERROR log line and an audit entry of the failed alert, and it tries the alert
again at the next removal of the source.

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
