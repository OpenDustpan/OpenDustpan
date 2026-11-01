# Gates and access limits on state writes

This guide shows what the server checks before it lets a person's own OpenTofu
CLI, or a call of the tool-neutral API, write state. It shows which writes the
server checks, the facts that a gate reads, what a refusal looks like, and what
the server records.

A gate or an access limit that nothing matches changes nothing. A server with no
gate and no access limit lets every write go on, as before.

## What the server checks

The server checks a write at the point `state.write`. It runs the check before
the write, and before the OpenTofu CLI changes any resource.

| Write | Route | Operation kind |
| --- | --- | --- |
| The lock of an OpenTofu workspace | `POST /api/v2/workspaces/{id}/actions/lock` | `lock` |
| A force-unlock of a workspace | `POST /api/v2/workspaces/{id}/actions/force-unlock` | `unlock` |
| A restore of a state version | `POST /api/v2/state-versions/{id}/actions/restore` | `rollback` |
| The removal of a workspace | `DELETE /api/v2/organizations/{organization}/workspaces/{workspace}` | `delete` |
| The restore of a deleted unit | `POST /api/inventory/deleted-units/{tool}/{unitID}/restore` | `rollback` |
| The final delete of a deleted unit | `DELETE /api/inventory/deleted-units/{tool}/{unitID}` | `delete` |

OpenTofu writes a state version after it changed the resources. A refusal at
that time would leave resources that no stored state records. So the server
checks the lock, before any plan or apply. The lock request does not say if a
plan or an apply follows, so a gate at `state.write` stops both. The server
never refuses a state version of a client that holds a lock which passed the
check.

The server does not check these writes yet: a Pulumi update, stack import,
rename or removal, the OpenTofu HTTP backend, and a state version that a client
sends with no lock. They keep their earlier behavior.

A write with the token of a job passes the gates only in two cases. The job
decided the change before it started, so a phase that passed the apply decision
writes to a unit of its own job with no gate. Each other phase takes only the
lock of its plan. Each other write with a job token is denied, also a write to a
unit that the job does not hold. The access limits still hold for a job token.

The server cannot evaluate a gate for a job yet. So an enforced gate at
`state.write` refuses the start of a job on its unit, and it also refuses each
claim of a phase of a job that started before someone saved the gate. The refused
claim fails the job, with a reason that names the gate, and writes a denied
`runner:claim` audit entry. A gate in evaluate mode refuses neither.

## What a gate reads

A gate at `state.write` is a Cedar policy over these facts:

| Fact | Meaning |
| --- | --- |
| `principal.kind` | `person` or `service`. A principal of an unknown kind counts as `person`. |
| `principal.credential` | `session`, `access-token`, `job-token` or `trust-token`. |
| `source.kind` | `cli` for a write from the CLI of a tool, `api` for a call of the tool-neutral API. |
| `unit.tool` | The tool code of the unit, for example `opentofu`. |
| `unit.collection` | The collection of the unit, or an empty text. |
| `unit.labels` | The labels of the unit, as `key=value` texts. |
| `operation.kind` | The operation kind in the table above. |
| `plan.available` | Always `false`: the CLI sends no plan. |
| `state.available` | Always `false`: the server holds no new state yet. |

A gate that needs a plan fact refuses such a write, because `plan.available` is
`false`.

The message of a gate can name a fact in double braces, for example
`Writes to {{unit.collection}} are frozen`. A message that names a path that is
not a fact stops the write with the error `gate.rule-failed`.

An access limit is one Cedar `forbid` policy over the principal, the action and
the unit. The server checks it with the action of the route, for example
`unit:write` for a lock.

## What a refusal looks like

A refusal always answers 403 on the OpenTofu routes. So OpenTofu does not read
it as a held lock, does not retry it, and holds no lock. The message holds the
message of each gate that matched, the error code and the ID of the decision
record.

The TFE-compatible API answers a JSON:API document:

```json
{
  "errors": [{
    "status": "403",
    "code": "gate.denied",
    "title": "forbidden",
    "detail": "Writes to opentofu are frozen. (error gate.denied, decision record gdr-yzimt6xg4xkiu4ewx7ng)"
  }]
}
```

`tofu plan` and `tofu apply` print the text:

```text
Error: Error acquiring the state lock

Error message: forbidden

Writes to opentofu are frozen. (error gate.denied, decision record
gdr-yzimt6xg4xkiu4ewx7ng)
```

The tool-neutral inventory API answers `{"code": <status>, "message": "..."}`
with the same message text. It answers 403 for a deny or an access limit, 409 for
a gate in error, and 503 when the engine does not answer.

| Error code | Meaning |
| --- | --- |
| `gate.denied` | A gate in `enforce` mode denies the write, or an approval outcome matched. |
| `gate.limited` | An access limit forbids the call. |
| `gate.rule-failed` | A gate gave an error, or its message cannot render. |
| `gate.unavailable` | The policy engine did not answer. |

## Outcomes

- `deny`: the write is refused.
- `approval:<rule>`: a CLI cannot wait for people, so the write is refused. The
  message names the rule and the unit.
- `warn`: the write goes on. The warning is in the decision record only.
- A gate in `evaluate` mode is evaluated and recorded, and has no other effect.
- A gate in error stops the write when it is in `enforce` mode. A gate with
  `onError: skip` counts as no match. A gate in `evaluate` mode stops nothing.
- The server fails closed. An engine that does not answer, a store error and an
  unknown value all refuse the write.

## The decision record

Each check writes one decision record with an ID that starts with `gdr-`. The
record holds the time, the principal, the version of the fact schema
(`state.write/1.0`), the facts of each unit with their SHA-256, each gate that
the server evaluated with its version, mode and result, the approval
requirements, the warnings and the result: `allow`, `warn`, `awaiting-approval`,
`deny` or `error`. A refusal by an access limit shares one record with the
refusals of the same principal, action and limit within 60 seconds. The record
keeps a count.

## Limits of this version

- The [gate configuration API](../reference/gate-config-api.md) stores gates and
  access limits for a namespace, a collection and a unit. It has no route for
  ownership rules or path sets yet.
- No route reads decision records yet.
- The server knows no group of a principal and no time of the last
  authentication. A gate or a limit that reads a group does not match. A limit
  that asks for a recent authentication forbids the call.
- A break-glass for a gate in error is not available.
- An approval default does not apply at `state.write`.
