# Check a workspace for drift

This guide shows how to start a drift check of an OpenTofu workspace through the
REST API, and how to read the latest drift result of the workspace.

A drift check looks for a difference between the state of a workspace and the
real resources that the state names. It is a job with the operation `refresh`
and one phase, `plan`. For OpenTofu it is a refresh-only plan. It changes no
resource and writes no state version. A drift check never holds the workspace
for a change, so it never blocks an apply.

The paths in this guide are under `/api/dustpan/v1/`. They work with the same
token as the other tool-neutral APIs. See `docs/reference/drift-api.md` for the
list of paths and the fields.

## What works in this version

This version has the routes, the job and the store of the drift result. A
runner cannot record a drift result yet. The server needs two more parts before
a runner can do it: the routes that a runner uses to report the steps of a job,
and the step that reduces the plan to a list of addresses.

Because of this, a drift check that you start waits for a runner and ends with
no drift result. The drift read of the workspace keeps the status `unknown`.
The status `unknown` means that no check reported on the workspace. The server
never shows `in-sync` for a workspace that no check finished.

The drift check does not start on its own. The drift schedule, the `drifted`
badge in the inventory, the drift section of the context API and the
notification of a change of the drift status come in later versions.

## Who can start a drift check

A drift check needs the action `unit:run-plan` on the workspace. It also needs
`namespace:read` on the namespace and `unit:read` on the workspace. The
bootstrap administrator token holds these actions. Of the built-in roles,
`admin`, `account-administrator` and `site-administrator` hold them.

A caller that cannot read the workspace gets the answer 404 for both routes. The
answer is the same as for a workspace that does not exist. A caller that can read
the workspace and lacks `unit:run-plan` gets the answer 403 for the start.

## Find the unit ID

Both routes name the workspace by its unit ID and not by its name. The unit
list of the inventory API gives the ID of each workspace.

## Start a drift check

Send `POST /namespaces/{namespace}/units/{unit_id}/drift-checks`. The body is
optional. With no body, the server takes the only runner pool of the namespace
that runs jobs. It refuses the start when the unit has no code location, because
the server keeps no code location of a unit yet. So the body names a location:

```json
{
  "pool": "ci",
  "location": { "repository": "acme/infra", "ref": "main", "directory": "net" },
  "reason": "after the incident"
}
```

The fields of the body are the same as for a plan job. A request that names a
pool also needs `namespace:manage-runners`. A request that names a location also
needs `unit:define-steps`. A field that is not in this list fails the request
with 400. A client cannot set the operation, the steps, the source kind or the
purpose of the job. The server sets them.

The answer is 202 with the ID of the job:

```json
{ "jobId": "job-4f9c1d2e7a", "unit": { "id": "ws-1", "tool": "OpenTofu", "name": "network" } }
```

The `Location` header names the job. Read it with the jobs API. The job has the
operation `refresh`, the purpose `drift-check` and one phase, `plan`. It shows
the same states as any other job. A job that waits for a runner has the state
`dispatched`.

### Two starts in one minute give one job

The server makes one job for two starts of one workspace in the same minute by
the same caller. So a double click does not start two checks. A second start in
that minute with another pool, location or reason gets the answer 409 with the
code `drift.start-conflict`. The route takes no `Idempotency-Key`
header. A request with the header gets the answer 400.

### A manual lock queues the job

A manual lock on the workspace does not refuse the start. The job waits with the
state `queued` and the wait reason `locked`. A change freeze does not hold a drift
check, because a freeze holds only the phases that change something.

## The steps of the check

The server fixes the steps. For an OpenTofu workspace they are `tofu init` and
`tofu plan -refresh-only`. The check keeps the default state lock. It never uses
`-lock=false`. A check that cannot get the lock fails.

A drift check has no custom step and no hook. The runner starts it with no
variable that adds arguments to the tool, for example `TF_CLI_ARGS`.

## Read the latest drift result

Send `GET /namespaces/{namespace}/units/{unit_id}/drift`. The answer has the
status of the workspace and the latest drift result:

```json
{
  "unit": { "id": "ws-1", "tool": "OpenTofu", "name": "network" },
  "status": "drifted",
  "checkedAt": "2026-10-21T09:30:00Z",
  "jobId": "job-4f9c1d2e7a",
  "resources": [
    { "address": "aws_s3_bucket.logs", "kind": "changed" },
    { "address": "aws_instance.web", "kind": "deleted" }
  ],
  "truncated": false
}
```

| Status | Meaning |
| --- | --- |
| `drifted` | The latest check found at least one difference. |
| `in-sync` | The latest check found no difference. |
| `unknown` | No check finished for the workspace. The answer has no time, no job and no address. |

A resource has an address and a kind. The kind `changed` means that the real
resource differs from the state. The kind `deleted` means that the real resource
is gone. The result never holds the name or the value of an attribute. A result
lists at most 1,000 addresses. The field `truncated` is true when the check
found more.

The server sets the time of the result from the end of the plan phase of the
job. It does not take the time from the runner.

## What happens when a check fails

A drift check that fails or ends canceled does not change the latest drift
result or its time. The failure shows as a failed job. The next check that
succeeds replaces the result.

When the server deletes a workspace for good, it deletes the latest drift result
of the workspace with it. The backup of the installation holds the drift results.
