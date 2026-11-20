# Approval defaults and approval rules

This guide shows how a namespace administrator turns on approval before apply
for a namespace, and how to store named approval rules. It shows the requests,
the roles that they need, and what the server does with the result today.

Approval is off by default. A namespace with no approval default needs no
approval for any change.

## What the server does with these objects today

The routes below store, read and remove the objects. The server records each
write in the audit log of the namespace.

A job that changes something waits for approval when an approval default covers
it. A plan job changes nothing, so it needs no approval. A Pulumi update or an
OpenTofu run that a person starts with the CLI of the tool waits for no approval,
because the CLI cannot wait. A named approval rule has no effect until a gate
names it. Store the gate with the [gate configuration API](../reference/gate-config-api.md).

When the server has a job store, an approval default gives the phase that changes
something (apply, destroy, refresh or deliver) a gate. This rule holds for the
first version of that gate:

- The author of the job never counts as an approver of an approval default.
- The strictest default wins. A default of a collection or of a unit can raise
  the count of the namespace. It cannot lower it.
- A default that names no approvers lets any holder of the approve action, who is
  not the author, approve.
- A default that names groups lets only members of those groups approve.
- Each default is one requirement. If two defaults apply to one unit (for
  example, a default with 2 approvers of group `platform` in the namespace and a
  default with no approvers in a collection), the job gets one gate for each
  requirement. A unit has at most 3 requirements: one for the namespace, one for
  the collection and one for the unit. The gates have the names `default` (the
  defaults with no approvers, merged) and `default/<scope>` (a default that names
  groups).
- The approvers of all the gates of a job are distinct people. One person fills
  one place of one gate, also when that person is a member of the groups of two
  gates. In the example above, the job needs 3 people: 2 members of `platform`
  and 1 other person. The order of the approvals does not change the result.
- A default that names users or unit owners, that sets `freshAuthSeconds`, or
  that holds an `unless` body still makes the start of the job fail, also when it
  is the second requirement. The job approval does not enforce these yet. The
  start answers 503 with the code `gate.unavailable`, and the message names the
  requirement to change.

## A plan whose state changed

An apply refuses a plan whose state changed after the plan. This holds for an
OpenTofu workspace and a Pulumi stack.

The server compares the state version that the plan read with the current
version of the unit. When the two versions differ, the job goes back to its plan
phase and plans again. The reason of the job names the state version that the
plan read and the current version. The new plan needs approval again.

## Set the approval default of a namespace

`PUT /api/dustpan/v1/namespaces/<namespace>/approval-default` sets the default.
The request must come from a console session with a recent sign-in (see
[Roles and actions](#roles-and-actions)). The body holds the count of approvals
and the approvers:

```json
{"count": 2, "approvers": {"groups": ["platform"]}}
```

The server answers 200 with the stored default:

```json
{
  "count": 2,
  "approvers": {"groups": ["platform"], "users": [], "unitOwners": false},
  "freshAuthSeconds": null,
  "version": 1
}
```

| Field | Meaning |
| --- | --- |
| `count` | The number of approvals. Give 1 to 10. |
| `approvers.groups`, `approvers.users` | Who may approve. Give none to let any holder of the approve action approve. Give at most 100 names of at most 256 bytes. |
| `approvers.unitOwners` | Lets the owners of the unit approve. |
| `freshAuthSeconds` | The age that a sign-in must not exceed for an approval. Give `null` for none, 0 for a new sign-in at each approval, or 60 to 900 seconds. |
| `version` | The version of the stored default. The server sets it. |

The body takes no other field. A body with an unknown field answers 400. A
value outside its bounds answers 422 with the field in the message.

The server refuses approvers that no one can meet, and answers 422 with the
reason in the message. This holds for an approval default and for an approval
rule:

- A group name must be a valid group name, and the group must exist. Make the
  group first. A group with no member counts no one.
- A user name must not hold a control character or start or end with white
  space. The user does not need to exist yet: a person can sign in later.
- When `unitOwners` is false, `count` must not be more than the number of
  people who can approve. The server counts each named user once and the
  enabled members of each named group. A disabled principal does not count,
  and a member who left the group does not count. A person in two groups counts
  twice. When `freshAuthSeconds` is set, also to 0, an approval needs a new
  sign-in, which only a person gives, so the server counts only the persons
  among the members. With `freshAuthSeconds` null, a service principal or a
  workload with the approve action can approve too, so it counts. With
  `unitOwners` true, or with no approver named, the server checks no count,
  because the owners or the holders of the approve action are not known when
  you save.

The groups belong to the installation, not to the namespace. The server
finds each group by its name when you save, and stores the ID of the group
with the name. The ID is the identity of the group: the name is a label. When someone renames a group, the rule shows the new name and
keeps the group as an approver. When someone removes a group, the rule keeps
the old name and the group counts no one. A new group with the same name has
another ID, so it does not become an approver of the rule. To make the new
group an approver, save the rule again with the name of the group. When you do not
hold `server:read-principals`, a group that does not exist and a count above
the members of the named groups give one answer: 422 with the code
`gate.approvers-unmet`. The answer does not name the group or the number of
people. Lower the count, or ask an administrator to check the groups.

`GET` on the same path reads the default. It answers 404 when the namespace sets
none. The answer has an `ETag` header with the version.

A `PUT` with no `If-Match` header replaces the stored default, or creates it. A
`PUT` with `If-Match: "<version>"` replaces that version only. If another
request changed the default, the server answers 409 and changes nothing.

`DELETE` on the same path removes the default and answers 204. It answers 204
also when the namespace sets no default. After that, the namespace needs no
approval again. `DELETE` also takes `If-Match`. A namespace that does not
exist answers 404 to each of these requests.

## Store an approval rule

An approval rule has an ID, the approvers, a count and a timeout. A gate names a
rule as `approval:<id>`.

`PUT /api/dustpan/v1/namespaces/<namespace>/approval-rules/<id>` stores a rule.
It needs a console session with a recent sign-in. The body is:

```json
{"count": 2, "approvers": {"groups": ["platform"]}, "timeoutSeconds": 86400}
```

An access token can read a rule:

```sh
curl -H "Authorization: Bearer $DUSTPAN_TOKEN" \
  "https://dustpan.example/api/dustpan/v1/namespaces/acme/approval-rules/platform-review"
```

| Field | Meaning |
| --- | --- |
| `count` | The number of approvals. Give 1 to 10. |
| `approvers` | At least one group, user or `unitOwners`. |
| `authorMayApprove` | Lets the author approve. The default is `false`. |
| `freshAuthSeconds` | As for the default. |
| `timeoutSeconds` | The time that a change waits for approval. Give 3600 to 2592000. The default is 7 days. |

The ID in the path has 1 to 63 characters: lowercase letters, digits and `-`,
and it starts with a letter or a digit. The IDs `default` and `owners` are
reserved. The body must not hold an `id`.

- `GET .../approval-rules` lists the rules stored in this namespace, ordered by
  ID. It does not list rules of a wider scope. The answer is `{"rules":[...]}`.
  It answers 404 when the namespace does not exist.
- `GET .../approval-rules/<id>` reads one rule. It answers 404 for an unknown ID.
- `DELETE .../approval-rules/<id>` removes the rule and answers 204. If a stored
  gate names the rule, the server answers 422 and keeps the rule.

A namespace holds at most 64 approval rules. The 65th answers 422.

## Set an approval at a collection, a unit or the installation

The routes of the namespace have the same form at a collection and at a unit.
Put the scope in front of `approval-default` or `approval-rules`:

| Scope | Path prefix |
| --- | --- |
| A namespace | `/api/dustpan/v1/namespaces/<namespace>` |
| A collection | `/api/dustpan/v1/namespaces/<namespace>/collections/<collection>` |
| A stack or a workspace | `/api/dustpan/v1/namespaces/<namespace>/units/by-name/<tool>/<name>` |
| The installation | `/api/dustpan/v1` |

`<tool>` is `pulumi` or `opentofu`. Write the name of the unit with
percent-encoding: the stack `web/prod` is `web%2Fprod`. The default and the
rules of a unit stay with the unit, also after a rename. A new unit that has the
name of a removed unit starts with none.

The routes need the same actions, the same body, the same `If-Match` rules and
the same sign-in as the routes of the namespace. The installation has approval
rules and no approval default. Its routes need `installation:read-gates` and
`installation:manage-gates` (see [Roles and actions](#roles-and-actions)).

A list shows the rules that are stored at its own scope. The list of a collection
does not show the rules of its namespace, and the list of the installation
does not show the rules of a namespace. A read of the ID of a rule that another
scope holds answers 404. A narrower scope cannot take the ID of a rule of a wider
scope: the server answers 409 with the code `gate.final`.

Set `SCOPE` to one of the prefixes, with the server URL in front. Sign in as the
page [Gate configuration API](../reference/gate-config-api.md#sign-in-for-a-change)
shows, so that `session.txt`, `CSRF` and `SERVER` are set. These calls set a rule
and a default at a collection, and read them:

```sh
SCOPE=$SERVER/api/dustpan/v1/namespaces/acme/collections/payments

curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"count":1,"approvers":{"groups":["platform"]}}' \
  "$SCOPE/approval-rules/platform-review"

curl -b session.txt -X PUT -H "Origin: $SERVER" -H "X-Dustpan-Csrf: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"count":2,"approvers":{"groups":["platform"]}}' \
  "$SCOPE/approval-default"

curl -H "Authorization: Bearer $TOKEN" "$SCOPE/approval-rules"
curl -i -H "Authorization: Bearer $TOKEN" "$SCOPE/approval-default"
```

A `DELETE` of `$SCOPE/approval-default` resets the scope to no default. It answers
204 also when the scope sets none. The [reference page](../reference/gate-config-api.md#calls-to-copy)
has one call for each scope and each kind of object.

When a change answers 409 with the code `gate.version-conflict`, another request
changed the object. Read the object again, as the last call above does for the
default. The header `ETag` holds the current version. Repeat the change with
`If-Match: "<version>"`.

An access token never gives the recent sign-in that a change needs. A change with
a token answers 403 with the code `mfa.fresh-step-required`. Sign in to the console
again, and repeat the call with the session.

## Approve or reject a job that waits

A job that an approval default covers waits in the state `awaiting-approval`.
Each approver sends one request. The approver needs the action `job:approve` on
the stack or workspace of the job, and `job:read` to see the job.

First read the job. In its plan phase, take the `id` of the plan, the `sha256` of
the `plan-display` part, the `sha256` of the `plan-apply` part, and the
`stateVersion`. Then send the verdict with these values as the binding:

```text
POST /api/dustpan/v1/namespaces/acme/jobs/job-4kd9q2m7xw1ztr8bnhyc/approvals
{
  "verdict": "approve",
  "planBinding": {
    "plan": "pln-3fa7c1d0e9b24a68",
    "displaySha256": "5d41402abc4b2a76b9719d911017c592a0f1a5d0cc66a4fbb4a47ab1d53f1c8e",
    "applySha256": "7d793037a0760186574b0282f2f435e7b5e3b4d5a3c8c2a9f6c1e0d4b8a79f21",
    "stateVersion": "sv-17"
  },
  "comment": "The plan adds two subnets and changes nothing else."
}
```

The rules:

- A gate of two approvals needs two different people. A person who approves a
  second time gets 409 with the error code `job.already-approved`.
- A default that names groups lets only enabled members of those groups approve. A
  suspended membership gives no rights, so it does not count. The
  server compares the ID of the group, so a group that someone renamed still matches,
  and a group that someone made again with the same name does not. If the server
  cannot read the membership, it refuses the approval.
- The person who started the job cannot approve it, also when an agent started it
  for that person. That person can reject it.
- An agent cannot approve. An agent can reject.
- The binding must be the binding of the stored plan. If a new plan replaces the
  plan, the old approvals do not count, and the job waits again.
- The approval of a `destroy` job needs a fresh MFA step. This version cannot read
  the MFA step of a console session yet, so it refuses the approval of a destroy
  with 403 and the error code `mfa.fresh-step-required`. A rejection works.
- One `reject` ends the job in the state `rejected`. Any holder of `job:approve` on the
  unit can reject, also a person who is not in the groups of the default, because a
  rejection only stops the change.

When the last approval arrives, the job goes to `queued`, and the server dispatches
the apply phase when no freeze, lock or other change holds the unit. If the state of
the unit changed after the plan, the dispatch refuses the approvals, and the job plans
again. The
[jobs API reference](../reference/jobs-api.md) lists the errors.

### Destroy approval and the fresh step

An access token, a job token, a trust token or an agent cannot give the fresh
step for a destroy approval. A break-glass session does not bypass this check.
The refusal changes no approval and writes an approval denial to the audit log.

The refusal names `/api/server/sign-in/{provider}?fresh=1` for a new sign-in.
A person with no factor must first open the factor enrollment page. The first
factor needs an email code, a key that the Git host lists, or an enrollment
code from an administrator. A session alone is not proof of an enrolled factor.

This version does not pass session MFA proof to job approvals. A new sign-in
therefore does not make destroy approval work yet. Do not retry with an access
token or put an MFA time in the request body.

The composed plan and apply path is not available yet. The server starts plan
jobs only. It refuses runner apply and destroy starts, rather than omit a plan,
approval, limit or gate check.

## Roles and actions

| Step | Action | Object | Roles |
| --- | --- | --- | --- |
| Read a default or a rule | `namespace:read-gates` | The namespace | `viewer`, `secret-viewer`, `auditor`, `planner`, `writer`, `maintainer`, `admin`, `account-administrator`, `site-administrator` |
| Set or remove a default or a rule | `namespace:manage-gates` | The namespace | `admin`, `account-administrator`, `site-administrator` |
| Read a rule of the installation | `installation:read-gates` | The installation | `installation-operator`, `site-administrator` |
| Set or remove a rule of the installation | `installation:manage-gates` | The installation | `installation-operator`, `site-administrator` |

The actions of a namespace apply to the namespace of the path, also for a
collection or a unit of it. They give nothing on the installation: a holder of
`namespace:manage-gates` only gets 403 on a route of the installation, because a
rule of the installation acts on every namespace.

Only the `admin` role among the namespace roles can set or remove. A person
whom a rule gates can read that rule. A grant on another namespace gives nothing
here.

A change needs a console session with a recent sign-in. If the sign-in is older
than the freshness window of the installation, the server answers 401 with the
path of a new sign-in, and changes nothing. An access token never gives a
recent sign-in: a change with an access token answers 403 with the error code
`mfa.fresh-step-required`, changes nothing, and adds a failure entry to the
audit log. A read works with an access token.

## Errors

| Status | Cause |
| --- | --- |
| 400 | The body is not one JSON object, holds an unknown field, or is larger than 64 KiB. The error code is `request.body-invalid`. |
| 400 | The `If-Match` value is not one version in quotes, for example `abc` or `"0"`. The error code is `request.if-match-invalid`. |
| 401 | The request has no valid credential, or a console session must sign in again. |
| 403 | The caller lacks the action on this namespace or on the installation, or a change came with an access token. |
| 404 | The namespace, the default or the rule does not exist. |
| 409 | The stored version changed, or (rule PUT) a wider scope holds a rule with this ID. |
| 422 | A field is outside its bounds, the ID is not valid, or a stored gate names the rule. The approvers cannot be met, for example a group that does not exist or a count above the people who can approve. |

## The audit log

Each set and each removal adds one entry of the kind `change` to the audit log of
the namespace. The entry holds the caller, the object (for example
`namespace/acme/approval-default`) and the version change. A read adds no entry.
See [Read the audit log](audit-log.md).
