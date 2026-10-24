# Output sharing

This guide shows the actions that read the outputs of a stack or a workspace,
and the namespace setting that decides who can read the outputs of a new stack
or workspace.

A stack or a workspace can read the outputs of another one. A Pulumi stack
reference and an OpenTofu `terraform_remote_state` data source do this. The
stack or workspace that makes the outputs is the producer. The one that reads
them is the consumer.

## What this version does

This version adds the two actions of output sharing, the namespace setting, and
the read of outputs by a person.

- The server stores the setting and its audit entry.
- A person who holds `unit:read-outputs` reads the root outputs of the latest
  state of a stack or a workspace, and not the rest of the state.
- The server does not keep sharing rules yet. The value `namespace` of the
  setting has no effect until a later version adds sharing rules: no stack or
  workspace gets a rule now. A job token still reads the outputs of another
  stack or workspace with the grants of the person who started the job.

## The actions

| Action | What it allows | Built-in roles that hold it |
| --- | --- | --- |
| `unit:read-outputs` | Read the root outputs of the latest state of a stack or a workspace, and nothing else. The value of a secret output of a workspace also needs `unit:read-secrets`. | Each role that holds `unit:read`: `viewer`, `auditor`, `secret-viewer`, `writer`, `maintainer`, `admin`, and the account and site administrators |
| `unit:manage-sharing` | Save and remove sharing rules, and change the output sharing setting of a namespace | `admin`, and the account and site administrators |

`unit:read` does not include `unit:read-outputs`. A custom role that holds
`unit:read` and not `unit:read-outputs` cannot read outputs through a route
that reads outputs only. To give a person the outputs of a stack or a
workspace and not its whole state, make a custom role with `unit:read-outputs`
only.

## Read the outputs of a unit

The route answers the root outputs of the latest state of one stack or
workspace. It needs `unit:read-outputs` on the unit, and no other action. A
person who holds only that action reads the outputs through this route.

The latest state of a stack is the last checkpoint that an update saved. While
an update runs, the route does not answer the changes that the update has not
saved as a checkpoint yet.

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/units/$UNIT_ID/outputs
```

```json
{
  "tool": "opentofu",
  "unitId": "ws-0123456789abcdefghij",
  "stateId": "sv-0123456789abcdefghij",
  "outputs": [
    {"name": "db_password", "secret": true},
    {"name": "vpc_id", "secret": false, "value": "vpc-0abc"}
  ]
}
```

- `outputs` holds one entry for each root output, ordered by name.
- A secret output has `"secret": true` and no `value`. The route never answers
  the value of a secret output. For a stack, a secret in an object or a list
  of a plain output stays a marker with the path of the secret.
- `stateId` names the state that the outputs come from. A new state has a new
  ID.

### Errors

| Status | Cause |
| --- | --- |
| 404 | The unit does not exist, the unit is in another namespace than the path, or the caller does not hold `unit:read-outputs` on the unit. The three answers are the same. |
| 404 | The caller holds `unit:read-outputs`, and the stack or workspace has no state yet because no update or upload has written one. The message names the unit and says that it has no state yet. The request writes no denial entry. |
| 409 | The client encrypted the state of the workspace. The server cannot read the outputs of such a state. It answers 409 with the code `unit.client-encrypted` to every caller, also to a caller that holds `unit:read`. It never answers an empty list for such a state. |
| 409 | A new state came in while the server checked or read the outputs. Send the request again. |

## What a person with only `unit:read-outputs` can read

| Route | Answer |
| --- | --- |
| `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` | 200 |
| `GET /api/v2/workspaces/{id}/current-state-version-outputs` (OpenTofu) | 200. A sensitive output has no value in the list. |
| `GET /api/v2/state-version-outputs/{id}` of the current state version (OpenTofu) | 200 for a plain output. A sensitive output needs `unit:read-secrets` and answers 403 without it. |
| `GET /api/v2/state-version-outputs/{id}` of an older state version | 403. An older state can hold a value that the owner rotated, so this route needs `unit:read`, and `unit:read-secrets` for a sensitive output. |
| Every route that answers a whole state: the export of a stack, the current state version, the download and the list of state versions | 403 |

For OpenTofu, the 403 applies to every other route of the workspace, not only
to the routes of the state. A person who holds `unit:read-outputs` and no
other action on the workspace gets 403 where a person with no action on it
gets 404.

An OpenTofu `terraform_remote_state` data source and a Pulumi stack reference
read the whole state of the other unit. They need `unit:read` on it for a
person. A person who holds only `unit:read-outputs` cannot use them: a stack
reference fails with 403, and `terraform_remote_state` fails because that
person cannot read the organization. A later version adds the sharing rules
that let a job read the outputs of another unit.

A principal that holds `unit:read-outputs` and `unit:read` on a unit reads the
state as before.

### A state that the client encrypted

OpenTofu can encrypt the state on the client. The server cannot read such a
state, so it cannot reduce it to its outputs.

- `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` answers
  409 to every caller for a workspace with such a state, also to a person who
  holds `unit:read`.
- The outputs routes of the TFE-compatible API answer 409 to a person who
  holds `unit:read-outputs` and not `unit:read`. A person who holds
  `unit:read` reads the state as before.

### Audit

A read of outputs that the server refuses writes one audit entry with the
action `unit:read-outputs`, the outcome `denied` and the name of the check
that refused it. The entry is in the namespace of the unit. A read that the
server allows writes no entry, except the read of a sensitive output of a
workspace, which writes an entry of the kind `tool-operation`.

## The output sharing setting

The setting `outputSharing.newUnitDefault` of a namespace decides the sharing
rule of each new stack and workspace of the namespace.

| Value | What a new stack or workspace gets |
| --- | --- |
| `nobody` | No sharing rule. No job of another stack or workspace reads its outputs until a person saves a rule. This is the default. |
| `namespace` | One sharing rule that names its namespace. |

A namespace that never changed the setting reads `nobody`. A change of the
setting applies to the stacks and workspaces that the namespace makes later. It
changes no rule of a stack or workspace that exists.

### Read the setting

The read needs `namespace:read` on the namespace.

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/output-sharing
```

```json
{"newUnitDefault":"nobody"}
```

### Change the setting

The change needs `unit:manage-sharing` on the namespace. A grant on a stack,
a workspace or a collection does not reach the setting.

```sh
curl -sS -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"newUnitDefault":"namespace"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/output-sharing
```

The answer is 200 with the setting as stored:

```json
{"newUnitDefault":"namespace"}
```

### Errors

| Status | Cause |
| --- | --- |
| 400 | The body is not one JSON object with the field `newUnitDefault`, the value is not `nobody` or `namespace`, or the body has another field. The setting does not change. |
| 403 | The caller does not hold the action of the route on the namespace. The setting does not change. |
| 404 | The namespace does not exist. |
| 409 | The namespace moved to another region during the change. Send the request again. |

## Audit

Each change of the setting writes one audit entry in the namespace, in the
transaction of the change. The entry has the action `unit:manage-sharing`, the
namespace as its object, and the change of `outputSharing.newUnitDefault` from
the old value to the new value. A change by a caller without the action
writes an entry with the outcome `denied`. When the server refuses the same
principal and action again within 1 minute, it counts the refusal and writes no
new entry (see
[The limit on repeated denials](audit-log.md#the-limit-on-repeated-denials)).
A read writes no entry.

## Repair a stored value that is not valid

A read of a stored value that is not `nobody` or `namespace` answers 500 and
no value. A row of another version could hold such a value. To repair it, send
a change with a valid value. The change replaces the stored value. The audit
entry records the old value as `invalid:` followed by the quoted stored value.

## Backup and restore

A backup holds the setting of each namespace. A restore refuses a backup whose
setting of a namespace is not `nobody` or `namespace`.
