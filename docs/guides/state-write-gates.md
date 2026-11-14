# Gates and access limits on state writes

This guide shows what the server checks before it lets a person's own OpenTofu
CLI or Pulumi CLI, or a call of the tool-neutral API, write state. It shows which writes the
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
| The lock of the OpenTofu HTTP backend | `LOCK /tofu/state/{namespace}/{workspace}` | `lock` |
| A force-unlock on the HTTP backend: an `UNLOCK` by a principal that does not hold the lock | `UNLOCK /tofu/state/{namespace}/{workspace}` | `unlock` |
| A state version with no lock on the HTTP backend (no `?ID=`, through the unlocked override) | `POST /tofu/state/{namespace}/{workspace}` | `unlocked-write` |
| A Pulumi stack import: `pulumi stack import`, and `pulumi state delete`, `unprotect`, `move` and `rename` | `POST /api/stacks/{organization}/{project}/{stack}/import` | `state-import` |
| A restore of the checkpoint of a Pulumi stack version | `POST /api/stacks/{organization}/{project}/{stack}/restore/{version}` | `rollback` |
| A Pulumi stack rename: `pulumi stack rename` | `POST /api/stacks/{organization}/{project}/{stack}/rename` | `rename` |
| The removal of a Pulumi stack: `pulumi stack rm` | `DELETE /api/stacks/{organization}/{project}/{stack}` | `delete` |
| A Pulumi update cancel: `pulumi cancel` | `POST /api/stacks/{organization}/{project}/{stack}/update/{update}/cancel` | `unlock` |

OpenTofu writes a state version after it changed the resources. A refusal at
that time would leave resources that no stored state records. So the server
checks the lock, before any plan or apply. The lock request does not say if a
plan or an apply follows, so a gate at `state.write` stops both. The server
never refuses a state version of a client that holds a lock which passed the
check. A state version that names the lock, also a forced write, does not meet
the check again. An `UNLOCK` of the HTTP backend by the holder of the lock ends
its own lock and does not meet the check.

A state version with no lock on the HTTP backend holds no lock that met the
check, so the server checks it before its write. OpenTofu sends it after the
apply, so a refusal can leave resources that no stored state records. The
refusal says so.

The first `LOCK` of a workspace that does not exist creates the workspace
before the check, because a gate reads the facts of a unit that exists. A
refused first lock leaves the workspace with no state and no lock.

The gates of the source stack decide a Pulumi stack rename. The new name has no
gates of its own before the rename. Each `pulumi cancel` meets the check: the
CLI sends an access token and no lease, so the server cannot tell the holder of
the update from another person.

The server does not check these writes yet: a Pulumi update (the create of an
update, a refresh or a destroy) and a state version with no lock through the
TFE-compatible API. They keep their earlier behavior. The
`unchecked` list of the effective value names them (see
[Check that the gates are on](#check-that-the-gates-are-on)).

A write with the token of a job passes the gates only in two cases. The job
decided the change before it started, so a phase that passed the apply decision
writes to a unit of its own job with no gate. Each other phase takes only the
lock of its plan. Each other write with a job token is denied, also a write to a
unit that the job does not hold. The access limits still hold for a job token.

The server cannot evaluate a gate for a job yet. So an enforced gate at
`state.write` refuses the start of a job on its unit, and it also refuses each
claim of a phase of a job that started before someone saved the gate. The refused
claim fails the job, with a reason that names the gate, and writes a denied
`runner:claim` audit entry. The job does not wait for the gate to go. To run
the change, a person deletes the gate and starts a new run. A gate in evaluate
mode refuses neither.

## What a gate reads

A gate at `state.write` is a Cedar expression over these facts. Name each fact
with the `context.` prefix:

| Fact | Meaning |
| --- | --- |
| `context.principal.kind` | `person` or `service`. A principal of an unknown kind counts as `person`. The kind `agent` exists, but the server does not set it at `state.write` yet. |
| `context.principal.credential` | `session`, `access-token`, `job-token` or `trust-token`. |
| `context.source.kind` | `cli` for a write through the API of a tool: the TFE-compatible API, the OpenTofu HTTP backend or the Pulumi Cloud-compatible API. `api` for a call of the tool-neutral API. |
| `context.unit.tool` | The tool code of the unit, `opentofu` or `pulumi`. |
| `context.unit.collection` | The collection of the unit, or an empty text. |
| `context.unit.labels` | The labels of the unit, as `key=value` texts. |
| `context.operation.kind` | The operation kind in the table above. |
| `context.document.truncated` | The fact paths that the server emptied to fit the facts into 256 KiB. At `state.write` the only path is `unit.labels`. An empty list means that the server emptied nothing. |
| `context.plan.available` | Always `false`: the CLI sends no plan. |
| `context.state.available` | `true` when the server read the current state and the new state for this write. Only the restore of a state version fills it. For each other write it is `false` (see the table below). |
| `context.state.resources` | The count of resource instances in the new state. A resource with `count` or `for_each` counts once for each index key. `0` when `context.state.available` is `false`. |
| `context.state.removed` | The instance addresses in the current state and not in the new state, sorted. An address has the index key of its instance, for example `aws_instance.web[1]` or `aws_instance.web["a"]`, so the removal of `web[1]` to `web[9]` shows when `web[0]` stays. An empty list when `context.state.available` is `false`. A resource block with no instance (`count = 0`) has no address. |

A gate that reads `context.state.resources` or `context.state.removed` must
also read `context.state.available`. When the flag is `false`, the two facts
hold the zero value, and a condition such as `context.state.removed.contains("x")`
never matches. The condition does not fail and the server does not warn.

### Which write fills which fact

| Write | `state.available` | `state.resources` and `state.removed` |
| --- | --- | --- |
| The restore of a state version (`rollback`, TFE-compatible API) | `true` | Filled from the current state and from the state version that the restore brings back |
| The restore of a state version when the client encrypted the current state or the restored state | `false` | Zero value: the server cannot read the resources of an encrypted state. The decision record warns: `state.* facts are not available: state is client-encrypted`. The restore goes on under the gates that apply |
| The restore of a deleted unit (`rollback`, tool-neutral API) | `false` | Zero value: a deleted unit has no current state |
| The lock of a workspace (`lock`) | `false` | Zero value: the new state does not exist yet |
| A force-unlock (`unlock`) | `false` | Zero value |
| The removal of a workspace or the final delete of a deleted unit (`delete`) | `false` | Zero value |
| A state version with no lock (`unlocked-write`) | `false` | Zero value |
| A Pulumi stack import (`state-import`) or a restore of a Pulumi checkpoint (`rollback`) | `false` | Zero value: the server does not read the resources of a Pulumi state for the gates |
| A Pulumi stack rename (`rename`), a `pulumi stack rm` (`delete`) or a `pulumi cancel` (`unlock`) | `false` | Zero value: the write holds no new state |

The facts `context.plan.available` and `context.cost.available` are always
`false` at `state.write`, because the CLI sends no plan.

The server reads both states in the same request, just before it checks the
gates, and it writes the facts that it read into the decision record. The restore writes only
if the current state version is still the version that the facts came from. The
store checks this in the same transaction as the write, so a write by the same
lock holder cannot slip in between. If another version became current after the
gate decision, the server refuses the restore with `409` and the code
`workspace.changed-concurrently`, and writes a denied audit entry. Repeat the
restore to decide again.

A state that the client encrypted is a known condition, not an error: the
restore goes on, `context.state.available` is `false`, and the warning above is
in the decision record. The server reads a state as encrypted only when it is
exactly the encryption envelope of OpenTofu: the members `meta`,
`encrypted_data` and `encryption_version` with the value `v0`, and the plain
`serial` and `lineage`. A document that has the member `encrypted_data` and any
other shape is refused as not valid, so a plaintext state never reads as
encrypted and a state the server cannot classify never reads as plain.

The server refuses a restore when it cannot read one of the two states for
another reason: the store fails, the state holds a resource or an instance
entry that the server cannot read, or the state is above a limit. A state above
the size limit (`--max-state-bytes`), or with more than 100,000 resource
instances, gives the code `gate.limit`. Any other failure gives
`gate.unavailable`. Each refusal writes a denied audit entry. No gate sees an
empty fact for such a state. A server that runs with the gates off reads no
state for a restore.

The decision record of a restore holds `state.available`, `state.resources` and
`state.removed` under the facts of the unit, so a person can see which facts a
gate used.

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
with the scope of the gate. The rest of the message is the same at each scope:

- A namespace gate: `namespace/acme/x`.
- A collection gate: `collection/acme/payments/x`.
- A unit gate: `unit/acme/pulumi/web%2Fprod/x`.

A gate that needs a plan fact refuses such a write, because
`context.plan.available` is `false`.

The message of a gate can name a fact in double braces, for example
`Writes to {{unit.collection}} are frozen`. A message that names a path that is
not a fact stops the write with the error `gate.rule-failed`.

An access limit is one Cedar `forbid` policy over the principal, the action and
the unit. The server checks it with the action of the route, for example
`unit:write` for a lock. The groups of the principal are the names of its
groups. The server knows the time of the last authentication only for a
session. A session of a break-glass account is outside the access limits, and
an access token of that account is not.

A limit can test the group of the principal: `principal in
OpenDustpan::Group::"sre"`. The right side of that test must name a group: one
group entity, or a set of group entities. The save refuses any other right side
with `400` and the code `gate.limit-membership`, for example `principal in
resource.owners`. The server cannot check a change of the members of a group
against such a limit, so it refuses to store one.

A limit can name a group only when its author may manage groups with
`server:manage-groups`, or is the bootstrap administrator.

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

The OpenTofu HTTP backend answers 403 with the same text as a plain-text body:

```text
Writes to opentofu are frozen. (error gate.denied, decision record gdr-yzimt6xg4xkiu4ewx7ng)
```

A refused state version with no lock starts with this sentence: "The server
refused this state version, so resources may exist that no stored state
records."

OpenTofu does not print the body of a 403 from the HTTP backend. It prints one
of these texts:

| Command | Text |
| --- | --- |
| `tofu plan`, `tofu apply` (the lock) | `Error acquiring the state lock` and `HTTP remote state endpoint invalid auth` |
| `tofu state push -lock=false` | `Failed to persist state: HTTP error: 403` |
| `tofu force-unlock` | `Failed to unlock state: Unexpected HTTP response code 403` |

The Pulumi Cloud-compatible API answers 403 for each refusal, in the error
shape of the Pulumi CLI with the code of the catalog:

```json
{
  "message": "Writes to pulumi are frozen. (error gate.denied, decision record gdr-yzimt6xg4xkiu4ewx7ng)",
  "code": 403,
  "errorCode": "gate.denied"
}
```

The Pulumi CLI prints the status and the message:

```text
error: [403] Writes to pulumi are frozen. (error gate.denied, decision record gdr-yzimt6xg4xkiu4ewx7ng)
```

`pulumi stack import` and the `pulumi state` commands add the prefix
`could not import deployment:`. A refused `pulumi cancel` leaves the update
running, so the stack stays locked.

To free a stack when a gate refuses `pulumi cancel`, a person who may change
the gates of that stack edits the gate or deletes it. For example, the person
sets the `unless` clause of the gate to `context.operation.kind == "unlock"`.
Then `pulumi cancel` works again. The audit log records each change of a gate.

The server log has the line `state write refused` with the unit, the error
code and the ID of the decision record. The audit log has a `denied` entry
with the action of the write and the unit (see the [audit log](audit-log.md)).

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

## Check that the gates are on

The capabilities document shows the effective value of the gates to a
signed-in caller with the action `caller:read`:

```sh
curl -H "Authorization: Bearer $TOKEN" https://dustpan.example.com/api/dustpan/v1/capabilities
```

```json
{
  "apiVersion": "v1",
  "installationKind": "production",
  "stateWriteGates": {
    "mode": "on",
    "reason": "the store keeps gates: the server checks the write paths in checked, and does not check the write paths in unchecked yet",
    "checked": [
      "opentofu.tfe.lock",
      "opentofu.tfe.force-unlock",
      "opentofu.tfe.rollback",
      "opentofu.tfe.delete",
      "opentofu.http.lock",
      "opentofu.http.force-unlock",
      "opentofu.http.unlocked-write",
      "inventory.restore",
      "inventory.purge",
      "pulumi.import",
      "pulumi.restore",
      "pulumi.rename",
      "pulumi.delete",
      "pulumi.cancel"
    ],
    "unchecked": [
      "opentofu.tfe.state-version-without-lock",
      "pulumi.update"
    ]
  }
}
```

Each write path is `<tool>.<api>.<operation>`, or `inventory.<operation>` for
the tool-neutral inventory API:

| Write path | Write |
| --- | --- |
| `opentofu.tfe.lock`, `opentofu.tfe.force-unlock` | The lock and a force-unlock through the TFE-compatible API |
| `opentofu.tfe.rollback`, `opentofu.tfe.delete` | A restore of a state version and the removal of a workspace |
| `opentofu.http.lock`, `opentofu.http.force-unlock` | The `LOCK`, and an `UNLOCK` by a principal that does not hold the lock, on the HTTP backend |
| `opentofu.http.unlocked-write` | A state version with no lock on the HTTP backend |
| `inventory.restore`, `inventory.purge` | The restore and the final delete of a deleted unit |
| `opentofu.tfe.state-version-without-lock` | A state version with no lock through the TFE-compatible API |
| `pulumi.update` | The create of a Pulumi update, preview, refresh or destroy |
| `pulumi.import`, `pulumi.restore`, `pulumi.rename`, `pulumi.delete` | A Pulumi stack import, restore, rename or `pulumi stack rm` |
| `pulumi.cancel` | A Pulumi update cancel |

A change that adds the check of a write path moves it from `unchecked` to
`checked`. With the gates off, `checked` is empty and `unchecked` holds every
write path.

The two SQL stores keep gates, so `opendustpan serve` always runs with the
gates on. The gates are off only for a program that embeds the server with a
store that keeps no gates. Then `mode` is `off`, the reason names the store,
and no write is checked. The `store` of `opendustpan serve` must keep gates, so
a build of the binary with a store that keeps none does not compile. At each start, such a server writes the line
`state.write gates: off` with the reason to the log, and one audit entry in the
`@server` scope with the key `state.write gates`, the value `off` and the
reason.

The server does not start when the store keeps gates and the server cannot
check them, for example with no inventory reader. A runner claim follows the
same rule as a write from a CLI: with the gates on, an enforced gate refuses
the claim, and with the gates off, no gate covers a unit.

## Limits of this version

- The [gate configuration API](../reference/gate-config-api.md) stores gates and
  access limits for a namespace, a collection and a unit. It has no route for
  ownership rules or path sets yet.
- No route reads decision records yet.
- A break-glass for a gate in error is not available.
- An approval default does not apply at `state.write`.
