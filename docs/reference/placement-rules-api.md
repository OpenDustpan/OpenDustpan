# Placement rules API

A placement rule of a namespace chooses the collection of a new stack that the
Pulumi CLI makes. The Pulumi CLI sends only the namespace, the project name and
the stack name. It does not send a collection. The rules of the namespace
match the project name and the stack name. The first rule that matches names
the collection.

OpenTofu uses the same rules for a new workspace. A workspace has no project, so
the server matches a rule with the project `*`, and the `stack` pattern of the
rule matches the name of the workspace.

This page describes the rules and the routes that manage them. The server
applies the rules when the Pulumi CLI makes a stack, for example with
`pulumi stack init`. It also applies them to a stack that is made through the
Pulumi Cloud-compatible API. See [A new Pulumi stack](#a-new-pulumi-stack).

## What a rule does

A namespace has an ordered list of rules. Each rule has these fields:

| Field | Content |
| --- | --- |
| `id` | The stable ID of the rule, with the prefix `plr-`. |
| `namespace` | The name of the namespace. |
| `position` | The place of the rule in the list, from 1. The positions have no gap. |
| `tool` | The tool of the units that the rule places. The value is `pulumi` or `opentofu`. |
| `project` | The pattern for the Pulumi project name. |
| `stack` | The pattern for the stack name. |
| `collection` | The name of the collection for a stack that matches. The collection must exist. |
| `createdBy` | The subject that made the rule. |
| `createdAt` | The time of creation, in Unix seconds. |

To place a new stack, the server tests the rules from position 1. A rule
matches when its `tool` is the tool of the stack, its `project` pattern matches
the project name, and its `stack` pattern matches the stack name. The first
rule that matches wins. The server does not test the rules after it. When no
rule matches, the server chooses no collection. It refuses to make the stack
and it makes no unit. The error names the placement rules and tells the caller
to add a rule.

A rule never gives a right. The caller that makes the stack must still hold
`collection:create-unit` on the collection that the rule names.

A change to a rule does not change a stack that exists. A rule only applies to
a stack that is new.

## A new Pulumi stack

A request to make a stack goes through these steps in this order:

1. The server finds the first rule that matches the project name and the stack
   name. When no rule matches, the answer is `422` with the code
   `placement.no-match`. The server makes no stack.
2. The server checks that the caller holds `collection:create-unit` on the
   collection of the rule. Without it, the answer is `403`, and the server
   makes no stack. The server writes the refusal to the audit log.
3. The server checks that the collection exists. If it does not, the answer is
   `409` with the code `collection.not-found`, and the server makes no stack.
4. The server makes the stack, puts it in the collection and writes the audit
   entry in one transaction. The audit entry holds the rule ID, in the form
   `from=none;to=<collection>;rule=<rule ID>`. If any part fails, the server
   makes none of them, so no stack exists without a collection.

A server that keeps no placement rules makes no stack. The answer is `501` with
the code `placement.unavailable`.

The Pulumi CLI shows the message of the `422` answer. The server does not
choose a default collection. To allow every stack, add a last rule with the
patterns `*` and `*`.

## Patterns

A pattern is a glob. It is not a regular expression.

- A letter, a digit, `.`, `_` or `-` matches itself. The match is
  case-sensitive.
- `*` matches any run of characters, also an empty run.
- A pattern has 1 to 64 characters.
- A pattern has at most 3 `*` characters, and two `*` are never next to each
  other.
- A pattern has no other character. The server rejects a pattern with another
  character. It does not change the pattern.

The server tests a pattern against a name of at most 255 characters. A longer
name matches no rule. The run time of a match is at most the length of the
pattern times the length of the name, so a hostile pattern or name cannot make
the server slow.

Examples:

| Pattern | Matches | Does not match |
| --- | --- | --- |
| `payments` | `payments` | `Payments`, `payments-api` |
| `prod*` | `prod`, `prod-eu` | `preprod` |
| `*-prod` | `eu-prod` | `prod-eu` |
| `*` | every name | |

## Conflicts

A rule can be hidden. An earlier rule hides a later rule when every name that
the later rule matches is also matched by the earlier rule. A hidden rule never
matches, because the first match wins. Two rules with the same patterns hide
each other in the same way.

The server refuses a write that makes a hidden rule. The answer is 409 with the
`errorCode` `placement.conflict`. The message names the two rules. To fix it,
put the narrower rule before the wider rule, or narrow the patterns.

For example, the rule `payments` / `*` hides a later rule `payments` / `prod*`.
Put the rule for `prod*` first.

## Routes

All routes use the path
`/api/dustpan/v1/namespaces/{namespace}/placement-rules`. The caller signs in as
for the other APIs, with an access token or a console session. The object of
each action is the namespace.

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /placement-rules` | `namespace:read-placement-rules` | 200 with `{"rules": [...]}` in list order |
| `POST /placement-rules` | `namespace:manage-placement-rules` | 201 with the new rule |
| `GET /placement-rules/{rule}` | `namespace:read-placement-rules` | 200 with the rule |
| `PUT /placement-rules/{rule}` | `namespace:manage-placement-rules` | 200 with the rule |
| `DELETE /placement-rules/{rule}` | `namespace:manage-placement-rules` | 204 |

The built-in role `maintainer` holds the read action. The role `admin` holds
both actions. Every permission is a grant, so a person can list, audit and
revoke it.

### Make a rule

`POST` takes a JSON object. The fields `tool`, `project`, `stack` and
`collection` must be present. The field `position` is optional. Without it, the
rule goes to the end of the list. With it, the rule goes to that position, and
the rules from that position on move down by one place. The position must be
from 1 to the number of rules plus 1.

A namespace holds at most 200 rules.

### Change a rule

`PUT` replaces `tool`, `project`, `stack` and `collection`. The fields are
required, as for `POST`. A `position` moves the rule. Without `position`, the
rule keeps its place. The position must be from 1 to the number of rules.

### Delete a rule

`DELETE` deletes the rule and closes the gap in the positions. The stacks that
the rule placed keep their collections.

When someone deletes a collection, the server deletes the rules that name it in
the same transaction. The audit entry of the delete of the collection names each
deleted rule.

## Audit

Each write of a rule appends one audit entry with the action
`namespace:manage-placement-rules`. The `objectID` of the entry is the ID of
the rule. For a make or a change, the `version` of the entry gives the tool,
the patterns, the collection and the position.

## Example

```text
$ curl -s -X POST -H "Authorization: Bearer $TOKEN" \
    -d '{"tool":"pulumi","project":"payments","stack":"prod*","collection":"payments-prod"}' \
    https://dustpan.example.com/api/dustpan/v1/namespaces/acme/placement-rules
{"id":"plr-...","namespace":"acme","position":1,"tool":"pulumi","project":"payments","stack":"prod*","collection":"payments-prod","createdBy":"alex","createdAt":1767225600}

$ curl -s -X POST -H "Authorization: Bearer $TOKEN" \
    -d '{"tool":"pulumi","project":"payments","stack":"*","collection":"payments"}' \
    https://dustpan.example.com/api/dustpan/v1/namespaces/acme/placement-rules
{"id":"plr-...","namespace":"acme","position":2,"tool":"pulumi","project":"payments","stack":"*","collection":"payments","createdBy":"alex","createdAt":1767225601}
```

A stack `payments/prod-eu` gets the collection `payments-prod`. A stack
`payments/dev` gets the collection `payments`.

## Errors

Errors have the body `{"message": "...", "code": 400, "errorCode": "..."}`.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.body-invalid` | The body is not one JSON object. It has an unknown field, a missing field, or data after the object. |
| 400 | `request.invalid-path` | The rule ID in the path has an invalid form. |
| 400 | `placement.invalid-pattern` | A pattern fails the rules above. The message names the pattern. |
| 400 | `placement.invalid-tool` | The tool has no placement rules. |
| 400 | `placement.invalid-position` | The position is outside the list. |
| 400 | `collection.invalid-name` | The collection name is not valid. |
| 401 | | The request has no valid credential. |
| 403 | | The caller lacks the action of the route. |
| 404 | `namespace.not-found` | The namespace does not exist. |
| 404 | `placement.rule-not-found` | The namespace holds no rule with the ID. |
| 404 | `collection.not-found` | The collection does not exist in the namespace. |
| 409 | `placement.conflict` | An earlier rule hides the rule, or the rule hides a later rule. |
| 409 | `placement.too-many-rules` | The namespace holds 200 rules. |
| 501 | `placement.unavailable` | The server keeps no placement rules, so it cannot place a new stack. This answer comes from the stack routes. |
| 422 | `placement.no-match` | A request to make a stack matched no rule. This answer comes from the stack routes, not from the rule routes. |
