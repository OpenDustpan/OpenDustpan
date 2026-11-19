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
