# Run triggers

This guide shows how to save a trigger on a stack or a workspace, how to fire it
by hand, and what the server checks at each step.

A trigger is a rule of one stack or workspace that starts a plan of that stack
or workspace. A trigger has one kind:

- `follows`: a plan starts after another stack or workspace, the source, applies.
  A workspace of OpenTofu can follow a stack of the Pulumi CLI, and the reverse.
- `schedule`: a plan starts at the times of a frequency.
- `review-request-push`: a plan starts when a push changes a review request that
  has code of the stack or workspace.

A trigger starts a plan and nothing else. It cannot start an apply, a refresh or
a destroy. The plan is a job like any other job, so the gates, the approval
rules, the change freezes and the manual locks of the namespace apply to it.

## What this version does

This version saves, reads, lists, replaces and deletes triggers of all three
kinds, and fires a trigger by hand. A trigger starts a plan in this version only
when a person fires it. The server keeps the other starts for later: a `follows`
trigger does not yet start a plan after an apply, a `schedule` trigger does not
yet start a plan at its times, and a `review-request-push` trigger does not yet
start a plan on a push. The server refuses a loop of `follows` triggers when you
save one, so a trigger that you save now cannot make a loop later.

A stack or a workspace has no code location in its settings yet. A plan needs a
code location, so a fire names one in its body (see "Fire a trigger").

## Who can do what

| Action | Needs on the stack or workspace |
| --- | --- |
| Read or list triggers | `unit:read` |
| Save, replace or delete a trigger | `unit:manage` and `unit:run-plan`. A delete needs `unit:manage` only. |
| Save a `follows` trigger | `unit:read` on the source, too. Without it, a person could learn when a stack applies without the right to read the stack. |
| Fire a trigger | `unit:run-plan`. A fire with a code location needs `unit:define-steps`, as a plan job that you start with the jobs API. |

The built-in role `admin` of a namespace holds these actions. A caller who
lacks `unit:read` gets 404, the same answer as for a trigger that does not
exist. A caller who can read but lacks an action gets 403.

## The last saver

The server records the person who saved a trigger last: the last saver. You do
not send it. The server takes it from your sign-in.

The last saver must have a stored principal. A token of the bootstrap
administrator has no principal record, so a trigger that it saves would never
start. The server refuses that save with 403 and the code
`trigger.saver-denied`. Sign in as a person who has a principal record, or use
a token of such a person.

The server checks the last saver again at each start. A start needs that the
last saver still holds `unit:manage` and `unit:run-plan` on the stack or
workspace, and that the principal is not deleted or disabled. If the last saver
lost access, the server refuses the start with 403 and the code
`trigger.saver-denied`, and it writes an audit entry of the refusal. The
document of the trigger then has `needsNewSave: true`. A person who holds the
actions must save the trigger again, and so becomes its last saver. This holds
for a fire by hand too: a trigger that nobody maintains starts nothing.

The trigger keeps the ID of the principal record of its last saver, as well as
the subject. If a person leaves and a new person gets the same subject, the
new person is not the last saver. The server refuses the start with the same
code, and a person who holds the actions must save the trigger again.

## Save a trigger

Send `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers`.
The `unit_id` is the stable ID of the stack or workspace, which the inventory
API gives. The body is one JSON object:

```json
{"kind": "schedule", "frequency": {"preset": "daily"}}
```

| Field | Content |
| --- | --- |
| `kind` | `follows`, `schedule` or `review-request-push` |
| `frequency` | For `schedule` only. Either `preset` (`hourly`, `every-6-hours`, `daily` or `weekly`) or `cron`, a custom expression of 5 fields in UTC. A custom expression cannot start a plan more than once an hour. |
| `source` | For `follows` only. `id`: the stable ID of the source, which must be in the same namespace. |
| `enabled` | Optional. The default is `true`. A trigger that is off keeps its rule and starts nothing. |

A field that is not in the table fails the request with 400. A trigger belongs
to one unit and one kind. `PUT .../triggers/{trigger_id}` replaces the rule, and
it cannot move the trigger to another stack or workspace or change its kind.

The answer is 201 with the trigger and a `Location` header. A trigger has an ID
that starts with `trg-`. A schedule answer has `frequency.expression`: the
cron expression, in UTC, that the schedule runs at. Read it to see the minute
and the hour of a preset.

### A loop of units

A `follows` trigger that closes a loop is refused with 409 and the code
`trigger.cycle`. A loop is a chain of `follows` triggers that leads back to the
first stack or workspace, also across tools. The answer lists the units of the
loop in order, with the name that the tool of each unit uses. A stack or
workspace that you cannot read is not in the list: the message gives only the
number of such units. A trigger that is
off closes no loop. When you switch it on, the server checks it again.

## Delete a trigger

Send `DELETE .../triggers/{trigger_id}`. The answer is 204. A stored trigger can
fail the checks of the server, for example after a restore of a backup that
another version made. The server does not serve such a trigger, and a read, a
replace or a fire of it fails. It starts no plan, and it takes no part in the
check for a loop of units. A delete deletes a trigger whose kind, frequency,
source, enabled flag, last saver or last saver ID is not valid. The trigger must
still name the live unit of the path: a trigger whose unit ID or namespace does
not match the unit of the path, or whose ID is not valid, is not reachable
through this route. A delete needs `unit:manage` on the unit of the trigger.

## Fire a trigger

Send `POST .../triggers/{trigger_id}/fire`. The body is optional:

```json
{"reason": "test run", "location": {"repository": "acme/infra", "ref": "main", "directory": "net"}}
```

| Field | Content |
| --- | --- |
| `reason` | Optional text for the audit entry. It has at most 200 characters and no control character. |
| `location` | Where the program and the config are in git: `repository` and `ref`, and optionally `connection`, `directory` and `revision`. A fire needs it until the settings of a stack or workspace hold a code location. |

A fire starts one plan job as you. The job service checks your actions and
applies the gates, the approval rules, the freezes and the manual locks. The
answer is 201 with `job.id` and a `Location` header of the job. The job keeps
the source `api`, the trigger ID and the last saver of the trigger, and the
[jobs API](../reference/jobs-api.md) shows both in the field `trigger`.

Send an `Idempotency-Key` header to make a retry safe. Two fires of one trigger
by one person with one key give the same job, also when they come in different
minutes. A second fire with that key and another reason or another location
fails with 409 and the code `trigger.fire-conflict`. The key has 1 to 255
printable ASCII characters. The answer to a second fire does not carry the
header `Idempotent-Replayed`. Another key starts another job.

Without a key, a double click starts one job: two fires of one trigger by one
person within the same minute give the same job. A double click that crosses a
minute boundary can start two jobs. Two people who fire one trigger in one
minute start two jobs.

| Status | Code | Meaning |
| --- | --- | --- |
| 409 | `trigger.disabled` | The trigger is off. |
| 403 | `trigger.saver-denied` | The last saver lost access. Save the trigger again. |
| 409 | `trigger.fire-conflict` | See above. |
| 409 | `job.frozen` | A change freeze of the namespace is active. |
| 422 | `job.unsupported` | The server cannot start the plan, for example for a unit of a tool that has no plan step. |
| 429 | `job.limit` | The stack or workspace has too many open jobs. |

A manual lock on the unit does not make the fire fail. The job waits with the
wait reason `locked` until a person deletes the lock.

## The namespace service principal

A start that no person makes needs a principal. Every namespace has one built-in
principal for it: the namespace service principal. The subject of the principal
is `system:namespace-service:` and the name of the namespace, and it shows
as the subject in the audit entry of such a start, with the trigger ID and the
last saver.

The principal has these limits:

- It holds `unit:run-plan` on the stacks and workspaces of its own namespace,
  and no other action.
- It has no token and no session. Nobody signs in as it, and no request can
  present it.
- No grant gives it more. The principal API refuses the subject, the grant API
  and the token API refuse the principal, and the check of the action ignores
  any grant, role or administrator flag that names it.

A plan that a trigger starts without a person does not read the outputs of
other units with the grants of that principal, because it holds no read action.
When such a plan reads the outputs of the source unit, the server uses the
current grants of the last saver. If the last saver lost `unit:read` on the
source, or if the principal of the last saver is missing, deleted or disabled,
the read fails. For a fire by hand, the grants of the caller and the grants of
the last saver must both allow the read, so a caller without access to the source
gains none through a trigger. The server does not yet read outputs of another
unit for a job, so this check has no effect in this version.

The principal does not hold `unit:read-secrets`. So a plan that a trigger starts
without a person can decrypt the secrets of its own unit only when its code
comes from a protected branch, and a plan that a push to a review request starts
gets none.

## Audit

Each save, replace and delete of a trigger writes an audit entry in the
namespace, with the action `unit:manage` and the trigger ID. Each start that the
check of the last saver refuses writes an entry with the outcome `denied`. The
audit entry of a start names the trigger and the last saver.
