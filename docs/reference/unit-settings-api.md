# Unit settings API

The settings of a unit tell the server where the code of the unit is, which
executor runs its jobs, and which runner pool and custom steps it uses. A unit
is a stack or a workspace. The server reads the settings of the unit at the
start of a job and fixes them in the job. A later change of the settings does
not change a job that exists.

Each unit has at most one settings record. A namespace has defaults for the
runner pool only. The default Git host connection belongs to the
[Git host connections](git-host-api.md) of the namespace. There is no merge by
field across levels: a field comes from the record of the unit, or from the next
level, or from a built-in value. Each answer shows the effective value of each
field and its origin.

## Paths

The paths of the API start with `/api/dustpan/v1`. Each path in this page is
relative to that prefix. The caller signs in as for the other APIs, with an
access token or a console session. `{unit_id}` is the stable ID of the stack or
workspace.

| Method and path | Actions | Answer |
| --- | --- | --- |
| `GET /namespaces/{namespace}/units/{unit_id}/settings` | `namespace:read`, `unit:read` | The settings, the effective values and the version |
| `PUT /namespaces/{namespace}/units/{unit_id}/settings` | `namespace:read`, `unit:read`, `unit:manage`, and the extra actions below | The stored settings |
| `DELETE /namespaces/{namespace}/units/{unit_id}/settings` | `namespace:read`, `unit:read`, `unit:manage` | Status 204 |
| `GET /namespaces/{namespace}/units/{unit_id}/settings/suggestions` | `namespace:read`, `unit:read` | The suggestions for the code location |
| `GET /namespaces/{namespace}/settings/unit-defaults` | `namespace:read` | The unit defaults of the namespace |
| `PUT /namespaces/{namespace}/settings/unit-defaults` | `namespace:read`, `namespace:admin` | The stored unit defaults |
| `POST /namespaces/{namespace}/unit-settings/bulk-put` | `namespace:read`, and the actions of a `PUT` for each unit | One result for each unit |

The server checks the actions in this order. A caller who lacks `namespace:read`
or `unit:read` gets 404, the same answer as for an object that does not exist.
This holds for a namespace or a unit that does not exist, a unit of another
namespace than the path, a deleted unit, and an ID that is not valid. A caller
who lacks another action gets 403, and the server writes an audit entry of the
refusal.

A `PUT` needs two more actions when its body asks for them:

| Action | When |
| --- | --- |
| `namespace:use-git-host-connection` on the namespace | The body names a connection that the record did not name, or the effective connection changes. |
| `unit:define-steps` on the unit | The body changes the custom steps. |

The built-in role `maintainer` holds `namespace:use-git-host-connection`.
The role `admin` holds every action.

No route of this API takes an `Idempotency-Key` header. A write with the header
fails with 400 and the code `request.idempotency-key-unsupported`.

## The settings document

An answer to a read or a write holds these fields.

| Field | Content |
| --- | --- |
| `unitID` | The stable ID of the unit |
| `namespace` | The name of the namespace |
| `version` | The number of writes of the record. It is 0 for a unit with no record. |
| `updatedAt` | The time of the last write, or an empty text for a unit with no record |
| `settings` | The fields of the record, as the table below shows. An empty field is a field that the record does not set. |
| `effective` | The effective value of each field, as `{"value": ..., "origin": ...}` |

The answer has the header `ETag` with the version in quotes, for example `"3"`.

### The fields of the record

| Field | Content | Effective value when the record sets none |
| --- | --- | --- |
| `executor` | `runner` or `write-back` | `runner` when the namespace has a runner pool for jobs, else `write-back` |
| `pool` | The ID of the runner pool for jobs | The default pool of the namespace, else none |
| `codeLocation.connection` | The ID of the Git host connection | The default connection of the namespace, else none |
| `codeLocation.repository` | The repository, as `owner/name`, or an `https` URL with no user name | None |
| `codeLocation.ref` | The branch that the unit deploys from | None |
| `codeLocation.directory` | The working directory: a clean relative path in the repository | `.` |
| `writeBack.delivery` | `review-request` or `direct-push` | `review-request` |
| `writeBack.files` | The config files of the unit that a write-back can edit, at most 16 | The default file of the tool: `Pulumi.<stack>.yaml` for a stack, `<workspace>.auto.tfvars.json` for a workspace |
| `reviewRequests.enabled` | `true` to turn pull request automation on | `false` |
| `steps` | The custom steps | None: the default steps of the tool |

A step has `name`, `role`, `phase`, `args`, `env`, `dir`, `timeoutSeconds`,
`allowFailure` and `outputs`. The server sets whether a step changes
resources, so the body has no field for it.

### The origin of a value

| Origin | Meaning |
| --- | --- |
| `unit` | The record of the unit sets the field. |
| `namespace-default` | The defaults of the namespace give the field. |
| `built-in-default` | No level sets the field, and the server gives its built-in value. An empty value means that nothing sets the field. |

A job that needs a field with no value fails at its start with a message that
names the field.

## Read

`GET .../settings` answers 200 for a unit with no record too. It then has
`version` 0, and every field comes from the namespace or from the built-in
value.

## Store

`PUT .../settings` replaces the whole record. The body is one JSON object of at
most 256 KiB with the fields `executor`, `pool`, `codeLocation`, `writeBack`,
`reviewRequests` and `steps`. A field that is not in the list fails the request
with 400. A body cannot set the unit, the version or the time.

The request must have the header `If-Match` with the version that the caller
read, in quotes. Use `"0"` for a unit with no record.

| Request | Answer |
| --- | --- |
| No `If-Match` header | 428, code `request.precondition-required` |
| A value that is not one version in quotes | 400, code `request.if-match-invalid` |
| A version that is not the stored version | 412, code `request.precondition-failed`. Read the settings again. |

A write that changes no field writes nothing: the version, the time and the
audit log stay as they are.

The server refuses a value and never changes it. A refusal has status 400 and the
code `unit-settings.invalid` for each of these:

- a `directory` or a file that is absolute, holds `..`, a control character or a
  backslash, or holds a part of CI configuration such as `.github`,
- a `ref`, a `repository` or a `connection` that starts with `-` or holds a
  control character or a space,
- a `repository` URL with a user name, a password, a query or a fragment,
- a value of `executor` or `delivery` that is not in the list,
- more than 16 files, or a file in the list twice,
- a step that does not pass the rules of a custom step, for example a variable
  name that carries a credential.

A write also fails with 422 in these cases:

| Code | Meaning |
| --- | --- |
| `unit-settings.connection-unknown` | The namespace has no connection with this ID. |
| `unit-settings.connection-lacks-capability` | The connection cannot do what the settings use it for. Write-back needs `files`, `compare` and `commit`, and a review request adds `review`. Pull request automation needs `events`. A connection with no push access has neither `commit` nor `review`. |
| `unit-settings.pool-unknown` | The namespace has no runner pool for jobs with this ID. |

The check of the capabilities reads the push flag of the connection. It makes no
call to the Git host.

Each write appends one audit entry in the transaction of the write. The entry has
the action `unit-settings-changed`, and one change for each field, with the old
and the new value. The entry holds the number of the steps and a digest of them,
and never an argument of a step.

## Reset

`DELETE .../settings` deletes the record at the version of `If-Match`. Every
field then comes from the namespace or from the built-in value. It answers 404
for a unit with no record. The audit entry has the action `unit-settings-reset`.

## Suggestions

`GET .../settings/suggestions` answers `{"suggestions": [...]}`. A suggestion
is a code location that the git details of a recent update of the unit name:
`repository`, `commit`, `source`, `updateId` and `observedAt`. The route reads
and writes nothing else. It never sets a field: a person accepts a suggestion by
saving the settings. The list is empty for a unit whose tool keeps no git details
of its runs, and for a server that keeps none.

## Unit defaults

`GET .../settings/unit-defaults` answers `namespace`, `pool`, `version` and
`updatedAt`, with the header `ETag`. `PUT` replaces the default pool. It has the
same `If-Match` rules as the settings, and its body is `{"pool": "<pool ID>"}`
or `{"pool": ""}` for none. The pool must run jobs in the same namespace. The
change applies to the jobs that start later, and it changes no record of a unit.
The audit entry has the action `unit-defaults-changed`.

## Bulk call

`POST .../unit-settings/bulk-put` stores one settings body as the settings of
many units. Use it to link many units to their code in one call.

```json
{
  "units": [{"id": "ws-1", "version": 0}, {"id": "ws-2", "version": 3}],
  "settings": {"codeLocation": {"repository": "acme/infra", "ref": "main"}}
}
```

`units` has 1 to 100 entries, and each has the stable unit ID and the version
that the caller read. A call with another number of units fails with 400 and the
code `unit-settings.bulk-size`.

The call is not atomic. For each unit, the server checks the actions, stores the
settings and writes the audit entry, as a `PUT` on that unit does. A unit that
fails does not stop or undo the others. The answer is 200 with one result for each
unit, in the order of the request:

```json
{"results": [
  {"id": "ws-1", "status": "ok", "version": 1},
  {"id": "ws-2", "status": "error", "version": 0,
   "error": {"code": 412, "errorCode": "request.precondition-failed", "message": "..."}}
]}
```

The `error` of a result is the error that a call on that unit alone answers. A
unit that does not exist and a unit that the caller cannot read give the same
error.

## Errors

The answers have the shape of the other tool-neutral APIs. See
[Error codes of the tool-neutral APIs](tool-neutral-error-codes.md). The codes
of this API:

| Code | Status | Meaning |
| --- | --- | --- |
| `unit-settings.not-found` | 404 | The namespace or the unit does not exist, the caller cannot read it, or a reset found no record. |
| `unit-settings.invalid` | 400 | A field of the settings is not valid. |
| `unit-settings.bulk-size` | 400 | A bulk call names no unit, or more than 100. |
| `unit-settings.connection-unknown` | 422 | The namespace has no connection with this ID. |
| `unit-settings.connection-lacks-capability` | 422 | The connection cannot do what the settings use it for. |
| `unit-settings.pool-unknown` | 422 | The namespace has no runner pool for jobs with this ID. |
