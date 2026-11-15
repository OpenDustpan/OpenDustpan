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

Send the verdict and the plan revision that the caller saw. A job that waits
for approval and has a newer plan revision answers 409. See
[approval rules](approval-rules.md) for the rules that open a gate.

## Confirm a job

A job that waits at a confirmation gate continues after one confirmation. The
caller needs the run action for the operation of the job. A job that does not
wait at a confirmation gate answers 409 with the code `job.wrong-state`.

## Redact a log

A caller with `unit:redact` on the unit removes a secret from the log of a job
with a post to the `log-redactions` route. A log that is in the wrong state
answers 409 with the code `job.wrong-state`.
