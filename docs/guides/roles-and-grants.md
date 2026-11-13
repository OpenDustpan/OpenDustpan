# Roles and grants

This guide shows how to read the built-in roles, make a custom role, give a
role to a principal or a group, and take a grant away.

A role is a named set of actions. A grant gives a role to a holder on an
object. The holder is a principal or a group. The object is the installation,
a namespace, a collection, a unit or a service principal. Every permission is a
grant, so a person can list, audit and revoke each one. A role does not
give a right by itself.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`. See
[Principals and groups](principals-and-groups.md) for the holders, and
[Access review](access-review.md) for the export of the grants.

## Read the roles

`GET /api/server/roles` lists the built-in roles and then the custom roles,
the custom roles ordered by name. Every signed-in principal can read the list.
The answer is not paged.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/server/roles
```

`GET /api/server/roles/{id}` returns one role. An unknown role gets `404`.

A role has an `id`, a `name`, a `description`, the list of `actions`, and a flag
that says whether the role is built in.

## Built-in roles

The server defines these roles. Their actions are fixed in the code. The
server does not store them, and an operator cannot change them.

| Role | What it holds |
| --- | --- |
| `viewer` | Read the namespace, the collections, the units, the outputs that the sharing rules allow, the profiles and the gates, and manage its own saved views. |
| `auditor` | Everything in `viewer`, plus the audit log and the access of each object. |
| `secret-viewer` | Everything in `viewer`, plus read the secrets of a unit. |
| `planner` | Everything in `viewer`, plus plan jobs and their logs. |
| `writer` | Everything in `viewer`, plus write, encrypt and decrypt state, read secrets and set the context of a unit. |
| `maintainer` | Everything in `writer`, plus manage, move, restore and lock units. It also manages collections and their units. It reads and uses the git host connections and the placement rules. |
| `admin` | Every action of the data family. This includes the management of access on the namespace. |
| `installation-operator` | Every action of the operator family for the installation. |
| `account-administrator` | The administration family, except the sign-in management and the access restore, plus the data family and the delegation family. It makes the tokens of each service principal whose reach is inside its own reach. |
| `site-administrator` | The operator, administration, data and delegation families. It holds every action except the actions of the caller's own access. |
| `token-manager` | The action `principal:manage-tokens`, which issues service tokens. |

The `planner` role cannot apply, destroy, approve, cancel or read secrets. The [planner role guide](planner-role.md) describes the role in detail.

The name `namespace-admin` is an alias of `admin`.

## Make a custom role

`POST /api/server/roles` makes a custom role. The caller needs
`server:manage-roles`, with a fresh sign-in. The answer is `201`.

| Field | Required | Rule |
| --- | --- | --- |
| `name` | yes | 1 to 64 characters. Letters, digits, `-`, `_` and `.` are allowed. The name cannot start with `.` or `-`. |
| `actions` | yes | At least one action. Each action must be in the action catalog. A caller cannot use the self actions. |
| `description` | no | At most 1000 bytes. |

A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. The body of the create is:

```json
{"name":"deployer","description":"Plans and reads the units","actions":["unit:run-plan","unit:read","unit:write"]}
```


These rules apply to the actions of a custom role:

- **The caller must hold every action.** A role can give no action that the
  caller does not hold on the installation. Otherwise the answer is `403`.
- **A name equal to a built-in name is refused with `400`.** The check does not
  depend on case.
- **A name that a custom role already uses is refused with `409`.** The check
  does not depend on case.

## Change a custom role

`PATCH /api/server/roles/{id}` changes `name`, `description` or `actions`. The
body must hold at least one of them. The caller needs `server:manage-roles`,
with a fresh sign-in.

- A built-in role cannot change. The answer is `409`.
- The caller must hold each action that the new list adds, and each action
  that the change takes away. Otherwise the answer is `403`.
- A role that a grant holds on an object other than the installation can keep
  only the data actions. Otherwise the answer is `409` with the message
  `role in use`.

## Delete a custom role

`DELETE /api/server/roles/{id}` answers `204`. The row stays for the history
of the access review.

- A built-in role cannot be deleted. The answer is `409`.
- A role that a current grant holds cannot be deleted. End the grants first.
  The answer is `409` with the message `role in use`.

## Give a role with a grant

A grant has a holder, a role, an object, a creator and a time. Each grant
lasts until it is revoked. A grant has no expiry in this release.

The holder is written as a subject:

| Holder | Form |
| --- | --- |
| A principal | `principal:<id>` |
| The members of a group | `group:<id>#member` |

The object is set by the route. The body does not name it.

| Object | List and create | Action | Revoke by path |
| --- | --- | --- | --- |
| The installation | `/api/server/grants` | `server:manage-access` | `DELETE /api/server/grants/{id}` |
| A namespace | `/api/server/namespaces/{namespace}/grants` | `namespace:manage-access` | `DELETE /api/server/namespaces/{namespace}/grants/{id}` |
| A collection | `/api/server/namespaces/{namespace}/collections/{collection}/grants` | `collection:manage-access` | `DELETE .../collections/{collection}/grants/{id}` |
| A unit | `/api/server/namespaces/{namespace}/unit-grants?tool=<tool>&unit=<name>` | `unit:manage-access` | `DELETE /api/server/namespaces/{namespace}/unit-grants/{id}` |
| A service principal | `/api/server/principals/{principal}/grants` | `server:manage-access` on the principal | `DELETE /api/server/principals/{principal}/grants/{id}` |

The unit route needs both `tool` and `unit`. Without them the answer is `400`.

Create a grant. The body holds `subject` and `role`.

A change that needs a fresh sign-in is made in the console. The console sends the session cookie `__Host-dustpan_session` and the `X-Dustpan-CSRF` header. A stored access token gets `403` with `mfa.fresh-step-required`. The body of the grant is:

```json
{"subject":"group:grp-...#member","role":"viewer"}
```


A grant answers `201` with the grant. The grant has these fields: `id` (with
the prefix `grt-`), `subject`, `role`, `object`, `createdAt` and `createdBy`.
The `object` is `@server`, a namespace name, `<namespace>/<collection>`, or a
unit reference.

Some rules apply to a create:

- The role must exist, or the answer is `404`.
- A role with operator or administration actions goes on the installation only.
- A grant on a service principal names a service principal, and it takes only
  delegation roles.
- A grant of a delegation role, such as `token-manager`, on a service principal
  needs a caller that holds the reach of that principal. Otherwise the answer is
  `403`. See [Access tokens](access-tokens.md) for the reach.
- The caller must hold every action of the role on that object. Otherwise the
  answer is `403`.
- A current grant with the same subject, role and object gets `409`.

A create on the installation, on a namespace, on a collection or on a service
principal needs a fresh sign-in. A stored access token gets `403` for these creates.
A create on a unit does not need a fresh sign-in.

## List and revoke grants

The list uses the same action as the create. A caller who can only read gets
`403`. The list takes `holder` and `role` filters. A `holder` takes the subject
form above. The list is paged, with `limit` and `cursor`. Each grant in the
list also shows `holderName` and `holderKind`.

Revoke a grant by its path, or by its ID alone with `DELETE /api/server/grants/{id}`.
The ID route needs the manage action of the grant's object, and the caller
must also hold the read action of that object. A caller without the read
action gets `404`, not `403`. A revoke answers `204`. The grant is ended, and
the history keeps it.

The revoke of the last site administrator is refused with `409` and the code
`access.last-site-administrator`.

## Ask why an action is allowed or denied

`GET /api/server/caller/access` answers for the caller. It lists the grants that
give the caller an action, and the roles that would give a missing action. Use
the `type` form. Give `type` as `namespace`, `collection`, `unit` or `principal`,
with an optional `action`. The `object` form is also accepted, but not with
`type` in the same request.

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://dustpan.example.com/api/server/caller/access?type=namespace&action=unit:write"
```

Any signed-in principal can read its own access. The caller only sees its own
grants here. For the grants of other principals, use the access review in
[Access review](access-review.md).

## Errors

Role and grant refusals carry a message and no `errorCode`. Read the status
code and the message.

| Status | Case |
| --- | --- |
| `400` | A role, a grant, a filter or a cursor is not valid. |
| `403` | The caller lacks an action that the role or the grant needs. |
| `404` | The role or the grant does not exist. |
| `409` | A name is taken, the role is built in or in use, or the grant already exists. |

Principal refusals, such as `principal.last-break-glass` and
`access.last-site-administrator`, carry an `errorCode`.
