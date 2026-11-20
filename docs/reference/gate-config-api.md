# Gate configuration API

A gate says whether a change may go on. An access limit takes away an action that
a grant gives. An approval rule names the approvers and the count that a gate
asks for. An approval default sets the approval for each change of a scope. The
gate configuration API stores, reads, lists, replaces and deletes these objects.
The [guide to gates and access limits on state writes](../guides/state-write-gates.md)
explains what the server checks and what a refusal looks like. The
[guide to approval defaults and approval rules](../guides/approval-rules.md)
explains what the approval objects do.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. A gate or an access limit has one scope: a namespace, a
collection of the namespace, a stack or workspace of the namespace, or the
installation. The scope is part of the path. `{scope}` stands for one of these
four prefixes:

- `/namespaces/{namespace}`
- `/namespaces/{namespace}/collections/{collection}`
- `/namespaces/{namespace}/units/by-name/{tool}/{name}`
- The installation: no prefix. The path of a route of the installation starts
  at the kind, for example `/gates/{id}`. See [The installation](#the-installation).

Write each variable segment with percent-encoding. `{tool}` is the code of the
tool: `pulumi` or `opentofu`. `{name}` is the name that the tool gives the unit.
The Pulumi stack `web/prod` of the namespace `acme` is
`/namespaces/acme/units/by-name/pulumi/web%2Fprod`. The server does not check
that the collection exists when it stores a gate.

The gates and the access limits of a stack or a workspace belong to the stack or
the workspace, not to its name. The server finds the stack or the workspace that
has the name now, and stores its stable ID. So:

- A rename of a Pulumi stack, a restore under a new name and a move to another
  collection keep the gates and the access limits of the stack or the workspace.
- A deleted stack or workspace keeps them until its final delete, which deletes
  them too. A restore gets them back. The routes of this page name a stack or a
  workspace by its name, so they do not reach the gates of a deleted one. The
  restore and the final delete of the deleted unit check them.
- A deleted unit's gates still count in the checks across scopes, for example a
  final gate of the namespace with the same ID, or the delete of an approval
  rule that one of them names. A refusal names such a gate by
  `deleted-unit/{tool}/{unitID}/{id}`, not by the name of the stack or the
  workspace, which a new one can have. The
  [deleted units](../guides/deleted-units.md) of the namespace list it by its
  tool and its `unitID`. Restore it to change its gates, or purge it to delete
  them.
- A new stack or workspace that has the name of a deleted one starts with none.
- A `PUT` for a name that no stack or workspace has now answers 404 with
  `unit.not-found`. A list for that name is empty, and a `GET` or a `DELETE` of
  one object answers 404 with `gate.not-found`.

The rows of this table hold for a namespace, a collection and a unit:

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET {scope}/gates` | `namespace:read-gates` | The gates stored at the scope, ordered by ID |
| `GET {scope}/gates/{id}` | `namespace:read-gates` | One gate |
| `PUT {scope}/gates/{id}` | `namespace:manage-gates` | The stored gate |
| `DELETE {scope}/gates/{id}` | `namespace:manage-gates` | Status 204 |
| `GET {scope}/access-limits` | `namespace:read-gates` | The access limits stored at the scope, ordered by ID |
| `GET {scope}/access-limits/{id}` | `namespace:read-gates` | One access limit |
| `PUT {scope}/access-limits/{id}` | `namespace:manage-gates` | The stored access limit |
| `DELETE {scope}/access-limits/{id}` | `namespace:manage-gates` | Status 204 |
| `GET {scope}/approval-rules` | `namespace:read-gates` | The approval rules stored at the scope, ordered by ID |
| `GET {scope}/approval-rules/{id}` | `namespace:read-gates` | One approval rule |
| `PUT {scope}/approval-rules/{id}` | `namespace:manage-gates` | The stored approval rule |
| `DELETE {scope}/approval-rules/{id}` | `namespace:manage-gates` | Status 204 |
| `GET {scope}/approval-default` | `namespace:read-gates` | The approval default of the scope, or 404 when it sets none |
| `PUT {scope}/approval-default` | `namespace:manage-gates` | The stored approval default |
| `DELETE {scope}/approval-default` | `namespace:manage-gates` | Status 204, also when the scope sets none |

Each action applies to the namespace of the path, also for a collection or a unit
of that namespace. A list shows the objects stored at its own scope. It does not
show the objects of a wider scope: the list of a collection does not show the
approval rules of its namespace. A `GET` of an ID that a wider scope holds
answers 404 with `gate.not-found`. The server serves these routes only when the
store keeps gates.

A change (`PUT` or `DELETE`) needs a console session with a recent sign-in. An
access token never gives a recent sign-in, so a change with a token fails with
403 and the code `mfa.fresh-step-required`. To change an object, sign in to the
console again and repeat the call with the session (see
[Sign in for a change](#sign-in-for-a-change)). A read works with a token. The
server writes an audit entry for each successful change.

## The installation

The installation holds gates, access limits and approval rules. They act on every
namespace. A route of the installation has no scope prefix. The installation
holds no approval default, so no route of the installation has one.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /gates`, `GET /access-limits`, `GET /approval-rules` | `installation:read-gates` | The objects stored at the installation, ordered by ID |
| `GET /gates/{id}`, `GET /access-limits/{id}`, `GET /approval-rules/{id}` | `installation:read-gates` | One object |
| `PUT /gates/{id}`, `PUT /access-limits/{id}`, `PUT /approval-rules/{id}` | `installation:manage-gates` | The stored object |
| `DELETE /gates/{id}`, `DELETE /access-limits/{id}`, `DELETE /approval-rules/{id}` | `installation:manage-gates` | Status 204 |

Both actions apply to the installation object. The installation operator and the
site administrator hold them. A holder of `namespace:read-gates` or
`namespace:manage-gates` only gets 403 with `access.denied` on these routes, and
a token of a namespace gets the same answer. The list of the installation shows
the objects of the installation only. It shows no object of a namespace. The rules
of versions, IDs, bodies and fresh sign-in on this page hold for these routes too.

## Sign in for a change

A change needs a console session. This example uses a local user with a password
([Local users with a password](local-users.md)). The person needs a role that holds
the action of the route, for example `admin` on the namespace or
`site-administrator`. `SERVER` is the public URL of the server:

```sh
SERVER=https://dustpan.example

curl -c session.txt -X POST -H "Content-Type: application/json" \
  -d '{"userName":"alice","password":"<password>"}' \
  "$SERVER/api/dustpan/v1/sign-in/password"

CSRF=$(curl -s -b session.txt "$SERVER/api/server/session" | sed -n 's/.*"csrfToken":"\([^"]*\)".*/\1/p')
```

A change sends the cookie, the header `Origin` with the URL of the server and the
header `X-Dustpan-Csrf` with the CSRF value. A read sends an access token:

```sh
TOKEN=<access token>
```

## Calls to copy

Set `SCOPE` to the scope of the object. Use one of these lines:

```sh
SCOPE=$SERVER/api/dustpan/v1/namespaces/acme
SCOPE=$SERVER/api/dustpan/v1/namespaces/acme/collections/payments
SCOPE=$SERVER/api/dustpan/v1/namespaces/acme/units/by-name/pulumi/web%2Fprod
SCOPE=$SERVER/api/dustpan/v1
```

Each call below has a read and a change. A change repeats the three headers. The
approval default has no installation row.

A gate:

```sh
curl -H "Authorization: Bearer $TOKEN" "$SCOPE/gates"
curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"decisionPoint":"state.write","condition":"context.operation.kind == \"unlock\"","outcome":"deny","message":"Unlocks are denied"}' \
  "$SCOPE/gates/no-unlock"
```

An access limit:

```sh
curl -H "Authorization: Bearer $TOKEN" "$SCOPE/access-limits"
curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"policy":"forbid (principal, action == OpenDustpan::Action::\"unit:write\", resource);"}' \
  "$SCOPE/access-limits/no-writes"
```

An approval rule. The group `platform` must exist:

```sh
curl -H "Authorization: Bearer $TOKEN" "$SCOPE/approval-rules"
curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"count":1,"approvers":{"groups":["platform"]}}' \
  "$SCOPE/approval-rules/platform-review"
```

An approval default, for a namespace, a collection or a unit:

```sh
curl -H "Authorization: Bearer $TOKEN" "$SCOPE/approval-default"
curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"count":1,"approvers":{"groups":["platform"]}}' \
  "$SCOPE/approval-default"
```

To delete an object, send `-X DELETE` with the three headers and the path of the
object. To delete an approval default, send `-X DELETE` to `$SCOPE/approval-default`.
It answers 204 also when the scope sets none.

## IDs

An ID has 1 to 63 characters: lowercase letters, digits and `-`, and it does not
start with `-`. An ID that starts with `builtin/` is for a gate that only the
server makes. A request cannot name it. The IDs `default` and `owners` are not
valid for an approval rule. A scope holds at most 256 gates, 64 access limits and
64 approval rules.

## Versions

Each answer for one object has the field `version` and the header `ETag` with the
same value in quotes. A new object has version 1.

- A `PUT` with the header `If-Match` replaces that version only. If the stored
  version is another one, the answer is 409 with `gate.version-conflict`. A read
  of the object answers the current version in the `ETag` header. Read it, then
  repeat the change with that version.
- A `PUT` without `If-Match` replaces the stored object, or makes it.
- A `DELETE` with `If-Match` deletes that version only. Without it, the
  `DELETE` deletes the stored version. For an object that does not exist, the
  answer is 404.

## The gate document

A request body is one JSON object of at most 64 KiB. A field that is not in the
list fails the request with 400. The body names no `id` and no scope: the path
names both.

| Field | Content |
| --- | --- |
| `decisionPoint` | The point where the gate runs: `change.propose`, `change.planned`, `change.apply`, `profile.save`, `profile.open` or `state.write` |
| `condition` | The body of the `when` clause, a Cedar expression over the facts of the decision point. Name each fact with the `context.` prefix, for example `context.unit.tool`. An empty value means true. At most 4 KiB. |
| `unless` | The body of the optional `unless` clause. At most 4 KiB. |
| `outcome` | `deny`, `warn` or `approval:<rule ID>` |
| `message` | The text that a refusal shows. It can name a fact as `{{unit.tool}}`. At most 1 KiB. A gate needs a message. |
| `mode` | `enforce` or `evaluate`. An empty value means `evaluate`: the match goes in the decision record and has no effect. |
| `onError` | `fail` or `skip`. An empty value means `fail`. |
| `final` | `true` stops a narrower scope from shadowing the gate or passing it with a break-glass |
| `reason` | A request only. The reason of a change that makes a final gate weaker. At most 1 KiB, in one line. |

An answer also holds `id`, `version`, and `checkedWith`: the `engine` and the
`schema` that the gate passed when the server saved it. An answer shows the
effective `mode` and `onError`, also when the request left them empty.

The server checks the condition and the `unless` clause against the fact schema
of the decision point before it stores the gate. A gate that does not parse, that
names an unknown fact, or that has a type error fails with 422. A gate cannot yet
read a path set, because no route stores path sets.

This gate body denies an unlock:

```json
{
  "decisionPoint": "state.write",
  "condition": "context.operation.kind == \"unlock\"",
  "outcome": "deny",
  "message": "Unlocks are denied"
}
```

A condition without the `context.` prefix does not parse. The request
`PUT /namespaces/acme/units/by-name/pulumi/web%2Fprod/gates/x` with the
condition `operation.kind == "unlock"` answers 422:

```json
{
  "message": "the gate configuration is not valid: unit/acme/pulumi/web%2Fprod/x: parser error: parse error at <input>:1:10 \".\": invalid primary",
  "code": 422,
  "errorCode": "gate.invalid"
}
```

The object in the message is the scope key of the gate, then its ID. It changes
with the scope of the gate, which is part of the path of the request. The rest of
the message is the same at each scope:

- A namespace gate, `PUT /namespaces/acme/gates/x`: `namespace/acme/x`.
- A collection gate, `PUT /namespaces/acme/collections/payments/gates/x`: `collection/acme/payments/x`.
- A unit gate, `PUT /namespaces/acme/units/by-name/pulumi/web%2Fprod/gates/x`: `unit/acme/pulumi/web%2Fprod/x`.

A change that makes a final gate weaker needs a `reason`. A change is weaker when
it switches the mode to `evaluate`, clears `final`, or changes the decision
point, the condition, `unless`, the outcome or `onError`. Without a reason, the
change fails with 409. Delete a final gate with the query parameter `reason`, for
example `DELETE .../gates/hold?reason=freeze%20ended`. The server writes the
reason in the audit entry.

A final gate of a wider scope stops a gate of a narrower scope with the same ID.
A gate with the outcome `approval:<rule ID>` needs the approval rule at its scope
or at a wider scope. The server refuses the delete of an approval rule that a
stored gate names.

## The access limit document

| Field | Content |
| --- | --- |
| `policy` | One Cedar `forbid` policy over the principal, the action and the resource. At most 4 KiB. |

An answer also holds `id` and `version`. The policy must have the effect
`forbid`. A policy with the effect `permit` fails with 422. A policy that names
an action that the server does not know also fails with 422.

## Audit entries

Each successful change at a namespace, a collection or a unit writes one audit
entry of the kind `change` in the audit log of the namespace. The entry holds the subject, the object, the old and the
new version, and, for a final gate, the reason.

| Change | Action in the entry |
| --- | --- |
| A set of a gate | `namespace:set-gate` |
| A delete of a gate | `namespace:delete-gate` |
| A set of an access limit | `namespace:set-access-limit` |
| A delete of an access limit | `namespace:delete-access-limit` |
| A set of an approval rule | `namespace:set-approval-rule` |
| A delete of an approval rule | `namespace:delete-approval-rule` |
| A set of an approval default | `namespace:set-approval-default` |
| A delete of an approval default | `namespace:delete-approval-default` |

A change at the installation writes its entry in the audit log of the installation
(`@server`), with the action `installation:set-gate`, `installation:delete-gate`,
`installation:set-access-limit`, `installation:delete-access-limit`,
`installation:set-approval-rule` or `installation:delete-approval-rule`. The entry
names the caller. The object of the entry is the scope key of the object, for
example `installation/gates/no-unlock`.

When a set changes the mode of a gate, the entry also holds the old and the new
mode, as the change `gate/<id>.mode`.

## Errors

Each answer holds the field `errorCode`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object, has a field that is not in the list, or names an `id` |
| 400 | `request.invalid-object` | The collection name, the tool or the unit name in the path is not valid |
| 400 | `request.if-match-invalid` | The `If-Match` header is not one version in quotes |
| 403 | `access.denied` | The caller lacks the action |
| 403 | `mfa.fresh-step-required` | The change has no recent sign-in, or the caller uses an access token |
| 404 | `gate.not-found` | The object does not exist at the scope |
| 404 | `unit.not-found` | A `PUT` names a stack or a workspace that does not exist now, or a deleted one |
| 409 | `gate.version-conflict` | The stored version is not the version of `If-Match` |
| 409 | `gate.final` | A final gate forbids the change, or the change needs a reason. For an approval rule: a wider scope holds a rule with the same ID. |
| 422 | `gate.invalid` | A field is not valid, an ID is not valid, or the engine refuses the policy. The message names the first problem. |
| 422 | `gate.limit` | A condition, an `unless` clause or a policy is over 4 KiB, or the scope holds the most objects of the kind. For approval rules the most is 64. |
| 503 | `gate.unavailable` | The server has no policy engine, or the engine does not answer. The server stores nothing. |
