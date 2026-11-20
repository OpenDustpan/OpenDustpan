# Jobs

A job runs a plan or an apply for one unit. These routes act on a job that
exists already. Each route needs an access token. A caller without `job:read`
on the unit gets the same 404 as for an unknown job.

| Route | Action | Object |
| --- | --- | --- |
| `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals` | `job:approve` | the unit of the job |
| `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel` | `job:cancel` | the unit of the job |
| `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/confirmations` | the run action of the job operation | the unit of the job |
| `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/log-redactions` | `unit:redact` | the unit of the job |

## Cancel a job

Send a body with an optional `reason`. The server writes the reason to the
audit log with the name of the caller.

```
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"reason":"wrong branch"}' \
  "$SERVER/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel"
```

A job that is canceling already stays as it is, and the call writes no second
entry. A job that ended, or whose change is in git for good, answers 409 with
the code `job.wrong-state`. The confirm and the redact routes answer the same
code for a job or a log that is in the wrong state. To see the
state of the job, run:

```
curl -H "Authorization: Bearer $TOKEN" "$SERVER/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}"
```

A caller with `job:read` but without `job:cancel` gets 403 with the code
`access.denied`, and the server records the denial.

## Damaged jobs

A damaged job is a stored job row that this version of the server cannot
read: the body of the row does not decode. The server never dispatches,
expires or closes a damaged job. It stays as it is until a person cancels it.

| Route | Answer for a damaged job |
| --- | --- |
| `GET .../jobs` | The job is not in `jobs`. The field `damaged` counts the damaged rows that the page read, for units that the caller may list. The count has no ID and no reason. A row that sits between the jobs of two pages can be in the count of both. |
| `GET .../jobs/{job_id}` and `GET .../jobs/{job_id}/logs/{phase}` | 409 with the code `job.damaged`. |
| `POST .../jobs/{job_id}/approvals`, `confirmations` and `log-redactions` | 409 with the code `job.damaged`. |
| `POST .../jobs/{job_id}/cancel` | Cancels the job. See below. |

The 409 has the same text each time. The text names the cancel route. It holds
no content of the row. A caller without `job:read` on the unit of the job, a damaged job of
another namespace, and a damaged job whose unit the server cannot find get the
same 404 as for an unknown job, so these answers never tell that the row
exists.

The list shows the number of damaged rows, not their IDs. When the artifact
sweep skips a damaged row, the server log has the message "sweep job artifact
blobs skipped a damaged job row" with the job ID.

To cancel a damaged job, send the same request as for any job:

```
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"reason":"damaged"}' \
  "$SERVER/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel"
```

The server finds the unit of the job from the unit ID column of the row, not
from the body. The caller needs `job:read` and `job:cancel` on that unit. A
row whose unit the server cannot find, a row of another namespace, and a
caller without `job:read` get the same 404 as for an unknown job. The server
changes the state of the row to `canceled` and writes the audit entry
`job:cancel` with the reason of the caller and the change `damaged`. It ends no
durable operation for the row. The answer holds the ID, the unit and the state
`canceled`. It holds no other stored value, because the server cannot read the
body of the row.

A second cancel of the same job answers 409 with the code `job.wrong-state`.
If the row was repaired while the server canceled it, the answer is 409 with
the code `job.conflict`. Send the request again.

## Approve a job

Send a JSON body with these fields:

| Field | Meaning |
| --- | --- |
| `verdict` | Required. `approve` or `reject`. |
| `planBinding` | The plan that the caller saw: `plan`, `displaySha256`, `applySha256` and `stateVersion`. Each text holds at most 128 bytes. A rejection needs no binding. |
| `comment` | An optional comment. It follows the rules of an audit reason. |

A body with a missing or unknown `verdict`, or with a value that is not valid,
answers 422 with the code `job.invalid`. The approval routes answer these
codes for a job that exists:

| Status | Code | Cause |
| --- | --- | --- |
| `409` | `job.finished` | The job ended already. |
| `409` | `job.not-awaiting-approval` | The job waits for no approval. |
| `409` | `job.plan-changed` | The plan of the body is not the stored plan. |
| `409` | `job.plan-stale` | The stored plan is too old, or was redacted. |
| `409` | `job.already-approved` | The caller approved this phase already. |
| `403` | `job.no-requirement` | The caller is a member of no group of any requirement of the phase. The message names the requirements. |

`GET .../jobs/{job_id}` lists the gates of each phase. Each gate has these
fields, in addition to `type` and `approvals`:

| Field | Meaning |
| --- | --- |
| `name` | The name of the requirement of the gate, for example `default` or `default/namespace/acme`. |
| `filled` | The number of places of the gate that the approvals fill. One person fills one place of one gate. |

Each approval has the field `for`: the names of the gates that the approval can
fill. They are the gates whose groups held the approver when the approver signed.
An approval with no `for` fills no place. The job runs when `filled` equals
`approvals` for every gate.

See [approval rules](approval-rules.md) for the rules that open a gate.

## Confirm a job

A job that waits at a confirmation gate continues after one confirmation. The
caller needs the run action for the operation of the job. Send a JSON body
with the field `verdict`, `approve` or `reject`. A body with no `verdict`, for
example `{}`, answers 422 with the code `job.invalid`. A rejection needs no
`planBinding`. A job that does not wait at a confirmation gate, and a job that
ended, answers 409 with the code `job.wrong-state`.

## Redact a log

The `log-redactions` route needs `unit:redact` on the unit. No role holds that
action now. Send a JSON body with these fields:

| Field | Meaning |
| --- | --- |
| `version` | Required. The ID of the log version, 1 to 64 bytes. |
| `substitutions` | Required. 1 to 100 objects, each with a `match` of 1 to 4096 bytes. |
| `reason` | Required. The reason, 1 to 1000 characters of plain text. |

The server answers in this order:

1. An unknown job, or a caller without `job:read` on the stack or workspace, answers 404.
2. A body that is not valid answers 400, for example a missing `reason`, a
   missing `version` or an empty `substitutions` list.
3. A valid request answers 422 with the code `job.unsupported`, until an
   artifact store adapter exists. The server keeps no store for job logs now,
   so it writes nothing.
