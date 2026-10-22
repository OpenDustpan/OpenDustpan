# Output sharing

This guide shows the actions that read the outputs of a stack or a workspace,
and the namespace setting that decides who can read the outputs of a new stack
or workspace.

A stack or a workspace can read the outputs of another one. A Pulumi stack
reference and an OpenTofu `terraform_remote_state` data source do this. The
stack or workspace that makes the outputs is the producer. The one that reads
them is the consumer.

## What this version does

This version adds the two actions of output sharing and the namespace setting.

- The server stores the setting and its audit entry.
- The server does not keep sharing rules yet. The value `namespace` of the
  setting has no effect until a later version adds sharing rules: no stack or
  workspace gets a rule now.
- A read of outputs still needs `unit:read`, as before. The route that reads
  the outputs only, with `unit:read-outputs`, comes in a later version.

## The actions

| Action | What it allows | Built-in roles that hold it |
| --- | --- | --- |
| `unit:read-outputs` | Read the root outputs of the latest state of a stack or a workspace, and nothing else. A secret output also needs `unit:read-secrets`. | Each role that holds `unit:read`: `viewer`, `auditor`, `secret-viewer`, `writer`, `maintainer`, `admin`, and the account and site administrators |
| `unit:manage-sharing` | Save and remove sharing rules, and change the output sharing setting of a namespace | `admin`, and the account and site administrators |

`unit:read` does not include `unit:read-outputs`. A custom role that holds
`unit:read` and not `unit:read-outputs` cannot read outputs through a route
that reads outputs only. To give a person the outputs of a stack or a
workspace and not its whole state, make a custom role with `unit:read-outputs`
only.

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
writes one entry with the outcome `denied`. A read writes no entry.

## Backup and restore

A backup holds the setting of each namespace. A restore refuses a backup whose
setting of a namespace is not `nobody` or `namespace`.
