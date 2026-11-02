# Gate configuration API

A gate says whether a change may go on. An access limit takes away an action that
a grant gives. The gate configuration API stores, reads, lists, replaces and
deletes both. The [guide to gates and access limits on state writes](../guides/state-write-gates.md)
explains what the server checks and what a refusal looks like. The
[guide to approval defaults and approval rules](../guides/approval-rules.md)
explains the approval routes that sit next to these routes.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. A gate or an access limit has one scope: a namespace, a
collection of the namespace, or a stack or workspace of the namespace. The scope
is part of the path. `{scope}` stands for one of these three prefixes:

- `/namespaces/{namespace}`
- `/namespaces/{namespace}/collections/{collection}`
- `/namespaces/{namespace}/units/by-name/{tool}/{name}`

Write each variable segment with percent-encoding. `{tool}` is the code of the
tool: `pulumi` or `opentofu`. `{name}` is the name that the tool gives the unit.
The Pulumi stack `web/prod` of the namespace `acme` is
`/namespaces/acme/units/by-name/pulumi/web%2Fprod`. The server does not check
that the collection or the unit exists when it stores a gate.

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

Each action applies to the namespace of the path, also for a collection or a unit
of that namespace. A list shows the objects stored at its own scope. It does not
show the objects of a wider scope. The server serves these routes only when the
store keeps gates.

A change (`PUT` or `DELETE`) needs a console session with a recent sign-in. An
access token never gives a recent sign-in, so a change with a token fails with
403 and the code `mfa.fresh-step-required`. A read works with a token. The
server writes an audit entry for each successful change.

## IDs

An ID has 1 to 63 characters: lowercase letters, digits and `-`, and it does not
start with `-`. An ID that starts with `builtin/` is for a gate that only the
server makes. A request cannot name it. A scope holds at most 256 gates and 64
access limits.

## Versions

Each answer for one object has the field `version` and the header `ETag` with the
same value in quotes. A new object has version 1.

- A `PUT` with the header `If-Match` replaces that version only. If the stored
  version is another one, the answer is 409.
- A `PUT` without `If-Match` replaces the stored object, or makes it.
- A `DELETE` with `If-Match` deletes that version only. Without it, the
  `DELETE` deletes the stored version. For an object that does not exist, the
  answer is 404.

## The gate document

A request body is one JSON object of at most 64 KiB. A field that is not in the
list fails the request with 400. The body names no `id`: the path names it.

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

Each successful change writes one audit entry of the kind `change` in the audit
log of the namespace. The entry holds the subject, the object, the old and the
new version, and, for a final gate, the reason.

| Change | Action in the entry |
| --- | --- |
| A set of a gate | `namespace:set-gate` |
| A delete of a gate | `namespace:delete-gate` |
| A set of an access limit | `namespace:set-access-limit` |
| A delete of an access limit | `namespace:delete-access-limit` |

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
| 409 | `gate.version-conflict` | The stored version is not the version of `If-Match` |
| 409 | `gate.final` | A final gate forbids the change, or the change needs a reason |
| 422 | `gate.invalid` | A field is not valid, an ID is not valid, or the engine refuses the policy. The message names the first problem. |
| 422 | `gate.limit` | A condition, an `unless` clause or a policy is over 4 KiB, or the scope holds the most objects of the kind |
| 503 | `gate.unavailable` | The server has no policy engine, or the engine does not answer. The server stores nothing. |
