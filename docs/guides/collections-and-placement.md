# Collections and placement rules

This guide shows how to make and delete a collection, how to move a unit into
a collection, and how to write the placement rules of a namespace.

A collection is a named set of units in one namespace. Each unit is in one
collection at most. Collections do not nest. A grant on a collection gives its
rights to the units in it. See [Roles and grants](roles-and-grants.md).

A placement rule chooses the collection of a new stack from its project name
and its stack name. See [Placement rules API](../reference/placement-rules-api.md)
for the pattern rules and the full list of the routes.

The examples use a token in `$TOKEN` and the host `dustpan.example.com`.

## Make a collection

`POST /api/dustpan/v1/namespaces/{namespace}/collections` makes a collection.
The caller needs `collection:create` on the namespace. The answer is `201`.

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"payments"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/collections
```

The body holds one field, `name`. Unknown fields are refused. The name rules
are these:

- 3 to 40 characters.
- ASCII letters, digits, `-`, `_` and a space between other characters.
- The first and the last character are a letter or a digit.
- Names are unique without regard to case. `Payments` and `payments` are the
  same name.

An invalid name gets `400` with the code `collection.invalid-name`. A name that
is taken gets `409` with the code `collection.already-exists`.

The answer holds `namespace`, `name` and `createdAt` in Unix seconds.

## Read collections

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/dustpan/v1/namespaces/{namespace}/collections` | `collection:read` on the namespace | `200` with `collections` |
| `GET /api/dustpan/v1/namespaces/{namespace}/collections/{collection}` | `collection:read` on the collection | `200` |

The list returns every collection of the namespace, ordered by name. It has no
paging parameters in this release.

## Delete a collection

`DELETE /api/dustpan/v1/namespaces/{namespace}/collections/{collection}` answers
`204`. The caller needs `collection:delete`.

A collection that still holds a live unit cannot be deleted. The answer is `409`
with the code `collection.not-empty`. Move the units first, as described below.
The server deletes the memberships of the soft-deleted units in the same
transaction.

## Move a unit into a collection

Move a unit with `PUT /api/inventory/unit-collection`. The route takes the unit
as a query value, and the new collection in the body:

```sh
curl -s -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"collection":"payments"}' \
  "https://dustpan.example.com/api/inventory/unit-collection?unit=pulumi:acme/payments-api"
```

The unit has the form `<tool>:<namespace>/<name>`. For a Pulumi stack, the tool
is `pulumi`.

The caller needs these actions:

| Action | On |
| --- | --- |
| `unit:move` | The unit |
| `unit:manage-access` | The unit |
| `collection:create-unit` | The target collection |
| `collection:manage-access` | The target collection |
| `collection:manage-access` | The source collection, when the unit is in one |

Moving a unit out of a collection that holds a manual lock also needs
`unit:manage` on the unit. See [Manual locks](manual-locks.md).

The answer holds the unit, the new collection and the previous collection:

```json
{"unit":"pulumi:acme/payments-api","collection":"payments","previousCollection":"billing"}
```

`previousCollection` is empty when the unit was in no collection. The audit
entry of the move names the source and the target.

A move that finds a different source than the one the caller expects gets `409`
with the code `collection.changed`. Read the unit again, and retry.

## Placement rules

A placement rule names the collection of a new stack. A rule has a `tool`, a
`project` pattern, a `stack` pattern and a `collection`. The rules of a namespace
are ordered. The first rule that matches a new stack names its collection. A
later rule does not apply.

A pattern is a glob. A `*` matches any run of characters, and nothing else is a
wildcard. The full rules are in the [placement rules reference](../reference/placement-rules-api.md).

When the Pulumi CLI makes a stack, for example with `pulumi stack init`, the
server puts the new stack in the collection of the first rule that matches. The
CLI sends no collection, and you do not set one.

- The caller needs `collection:create-unit` on that collection. Without it, the
  answer is `403`, and the server makes no stack.
- When no rule matches, the server refuses to make the stack. The answer is
  `422` with the code `placement.no-match`. The message names the placement
  rules and the route that adds a rule. The server does not use a default
  collection. To allow every stack, add a last rule with the patterns `*` and
  `*`.
- The server makes the stack and its place in the collection in one
  transaction. A stack never exists without its collection.
- A server that keeps no placement rules makes no stack. The answer is `501`
  with the code `placement.unavailable`.

### A new OpenTofu workspace

OpenTofu makes a workspace on first use, for example with `tofu init` or with
the first lock of a workspace. The server puts the new workspace in a collection
with the same rules. A workspace has no project, so the server matches the rules
with the project `*`. A rule for a workspace has the `tool` `opentofu`. The
`stack` pattern of the rule matches the name of the workspace.

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"tool":"opentofu","project":"*","stack":"*","collection":"platform"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/placement-rules
```

The refusals are the same as for a stack. The caller needs
`collection:create-unit` on the collection (`403` without it). When no rule
matches, the answer is `422` with the code `placement.no-match`, and the server
makes no workspace. A server that keeps no placement rules answers `501` with the
code `placement.unavailable`.

### A unit in no collection

A stack or a workspace that is in no collection takes no new write. The server
refuses the first lock and the state write of such a workspace with `409` and
the code `unit.no-collection`. The message names the command that moves the
unit into a collection: `PUT /api/inventory/unit-collection`. After the move,
repeat the request.

### Routes and actions

| Route | Action | Answer |
| --- | --- | --- |
| `GET /api/dustpan/v1/namespaces/{namespace}/placement-rules` | `namespace:read-placement-rules` | `200` with the rules in order |
| `POST /api/dustpan/v1/namespaces/{namespace}/placement-rules` | `namespace:manage-placement-rules` | `201` with the new rule |
| `GET /api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}` | `namespace:read-placement-rules` | `200` |
| `PUT /api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}` | `namespace:manage-placement-rules` | `200` |
| `DELETE /api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}` | `namespace:manage-placement-rules` | `204` |

### Add a rule

Without `position`, a new rule goes to the end of the list. With `position`, the
rule goes to that place, and the rules from that place move down by one.

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"tool":"pulumi","project":"payments","stack":"prod*","collection":"payments-prod"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/placement-rules
```

A namespace holds at most 200 rules.

### Order the rules

A rule that an earlier rule hides is refused with `409` and the code
`placement.conflict`. A rule is hidden when every name that it matches is also
matched by an earlier rule. Put the narrower rule first. For example, a rule
with the project `payments` and the stack `*` hides a later rule with the project
`payments` and the stack `prod*`. Put the `prod*` rule first.

### Change and delete a rule

`PUT` replaces the `tool`, `project`, `stack` and `collection`. All four are
required. A `position` moves the rule.

`DELETE` deletes the rule and closes the gap in the positions. The stacks that
the rule placed keep their collections. When a collection is deleted, the server
deletes the rules that name it in the same transaction.

### A rule does not give a right

A rule only chooses a collection. The caller that makes the stack still needs
`collection:create-unit` on that collection.

A rule change does not move a stack that exists. A rule applies only to a new
stack.

## Errors

| Code | Status | Meaning |
| --- | --- | --- |
| `collection.invalid-name` | `400` | The name does not follow the rules. |
| `collection.already-exists` | `409` | Another collection has the name, without regard to case. |
| `collection.not-found` | `404` | The collection does not exist. |
| `collection.not-empty` | `409` | The collection holds a live unit. |
| `collection.changed` | `409` | The source collection of a move is not the one the caller expects. |
| `unit.no-collection` | `409` | A step needs a unit that is in a collection, and the unit is in none. |
| `placement.conflict` | `409` | A rule is hidden by an earlier rule. |
| `placement.no-match` | `422` | No rule matches a new stack, so the server does not make it. |
| `placement.unavailable` | `501` | The server keeps no placement rules, so it cannot place a new stack. |
