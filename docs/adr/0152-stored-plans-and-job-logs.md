# 152. Stored plans and job logs

Status: accepted · 2026-10-17

## Context

[RFC 28](../rfc/0028-stored-plans.md) asks for stored plans, and
[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes runners and jobs.
Neither is accepted. A job runs a tool on a runner that the customer hosts.
The tool then talks to the server as usual: a Pulumi update or an OpenTofu
state version (RFC 6). A job makes two more kinds of content that the server
does not keep today: the plan, and the log of the job.

The project owner decided on 2026-10-17:

- The server stores plans, behind an action `read plans`. It keeps them for
  400 days. A self-hosted installation can change this period.
- The server keeps job logs for 400 days. A person can redact a job log.

The facts that the design must meet:

- A plan can hold plain secret values. An OpenTofu plan file holds every
  value of the plan, also sensitive values. Its JSON rendering marks
  sensitive values. A Pulumi update plan (`--save-plan`) holds the planned
  values, with secrets as ciphertext. The events of a Pulumi preview are the
  progress record of that preview.
- A job log is text that the tool and the runner write. The server cannot
  know which part of it is secret.
- ADR 150 calls a plan and a job log history items. ADR 151 gives each
  history item its own content key, and defines redaction.
- The glossary says "Preview" for the Pulumi operation and avoids "plan"
  there. User-facing text keeps each tool's own words (ADR 13).

## Decision

### Stored plans

1. **A stored plan belongs to a unit and a job.** A stored plan is the result
   of a plan operation that a job uploads: an OpenTofu `plan`, or a Pulumi
   `preview`. Shared code says "plan". The API and the console say "plan" for
   OpenTofu and "preview" for the Pulumi CLI. A plan has an ID with the prefix
   `pln-`, the unit (tool, namespace, unit ID), the job ID, the actor
   (ADR 150 decision 3), the creation time and the expiry time.
2. **A plan has parts.** Each part is one blob with a name, a size and a
   SHA-256. The tool package names the parts and owns their format. The core
   keeps them opaque (ADR 13). A part has a flag `secret` when it holds plain
   secret values.
   - OpenTofu: `plan` (the plan file, `secret`) and `json` (the JSON
     rendering).
   - Pulumi: `plan` (the update plan) and `events` (the engine events of the
     preview).
3. **Sealed with a content key.** Each plan has one content key (ADR 151).
   It seals every part. A redaction or the end of the retention period shreds
   it.
4. **Upload.** A job uploads a plan with `unit:write` on the unit. The upload
   is a fenced write in the namespace. The server writes each part blob
   first, then the plan row (ADR 26 decision 5). A job uploads a plan one
   time. The plan is immutable after the upload.
5. **Read.**
   - The metadata of a plan, the list of plans of a unit, and each part
     without the `secret` flag need `unit:read-plans`. A read of the JSON
     rendering masks sensitive values, with the masking rules of ADR 41 for
     the tool.
   - A part with the `secret` flag needs `unit:read-plans` and
     `unit:read-secrets`. Each such read writes an audit entry of the kind
     `tool-operation` before the answer (ADR 41 decision 5). The runner that
     applies a saved plan makes this read.
6. **A plan does not change the history of the unit.** A plan is not a state
   item (ADR 150 decision 1). Blame and the views at a time do not read
   plans.

### Job logs

7. **One log for each job.** A job log belongs to one job and one unit. The
   runner sends the log in chunks while the job runs. Each chunk has a
   sequence number, and the server stores it as one blob. A repeated chunk
   with the same number and the same bytes succeeds. A repeated chunk with
   the same number and other bytes answers 409. The end of the job closes the
   log, and the server then records the total size and the SHA-256 over the
   chunks in order. A closed log takes no more chunks.
8. **Sealed with a content key.** Each job log has one content key
   (ADR 151). It seals every chunk.
9. **Read.** A read of a job log needs `unit:read-job-logs` (question 1). The
   server does not mask a job log, because it cannot find secret values in
   text. A runner writes the log, so the runner masks the values that it
   knows as secret before it sends a chunk. RFC 6 decides how.
10. **Redaction.** A job log of a finished job can be redacted (ADR 151). A
    job log of a running job cannot. The answer is 409.

### Retention

11. **The periods.** The server keeps a plan and a job log for 400 days from
    its creation. Two settings change the periods for an installation (ADR 63):
    `--plan-retention` (`DUSTPAN_PLAN_RETENTION`) and `--job-log-retention`
    (`DUSTPAN_JOB_LOG_RETENTION`). The minimum is one day. A change of a
    setting writes an audit entry (ADR 63). A new period applies to the
    expiry time of new items only. The server stores the expiry time with
    each item, so a change never makes an item expire at once. The hosted
    distribution sets its own periods (ADR 59).
12. **The end of the period is a redaction.** The retention sweep finds each
    plan and each job log whose expiry time is in the past. It redacts each
    one through the path of ADR 151 decisions 13 and 14, with the actor
    `@server`, the cause `retention`, and the reason `retention period
    ended`. The metadata stays. A plan of a job that has not ended does not
    expire until the job ends.
13. **The metadata stays after the content.** The rows of expired and
    redacted plans and job logs stay until the final delete of the unit
    (ADR 47). They hold sizes, hashes, times and actors, never content
    (question 3).

### Routes and actions

14. **Routes wait for jobs.** The routes for plans and job logs land with the
    job API of RFC 6, in the tool-neutral OpenDustpan API. Each route lands
    with its e2e step first (ADR 12). This ADR fixes the storage, the keys,
    the retention and the actions now, so that RFC 6 builds on them.
15. **Actions.**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Upload a plan, send a job log chunk | `unit:write` | unit |
    | List plans, read plan metadata and parts without `secret` | `unit:read-plans` | unit |
    | Read a plan part with `secret` | `unit:read-plans`, `unit:read-secrets` | unit |
    | Read a job log | `unit:read-job-logs` | unit |
    | Redact a plan or a job log | `unit:redact` | unit |
    | Change a retention period | `server:manage-settings` | `@server` |

    `unit:read-plans` and `unit:read-job-logs` are new actions. A role
    (ADR 52) can give `unit:read` without them.

## Relation to the RFCs

- **RFC 28** asks how the server stores a plan and whether a plan is covered
  by the rule on state. This ADR stores a plan as blobs with hashes, a
  retention period and its own action, and seals it like state. Plan-bound
  approvals stay in RFC 28 (question 4).
- **RFC 6** is not accepted. This ADR fixes the storage, the keys, the
  retention and the actions, and the routes land with the job API.
- **RFC 39** gives the masking rules that the read of the JSON rendering of a
  plan uses.

This ADR supersedes no decision.

## Consequences

- Plans and job logs grow the blob store until their retention period ends.
  The sizing guide states the growth for each job.
- A plan file of OpenTofu holds plain secret values, sealed at rest under a
  content key that only the server opens. The read path for it is the
  path of ADR 41 for secret values, with an audit entry.
- The retention sweep writes one audit entry for each expired item.
- The `history` package holds the plan and job log types and ports
  (`PlanStore`, `JobLogStore`), with the conformance suites
  `historytest.RunPlans` and `historytest.RunJobLogs`. The `Retention` type
  holds the two periods.
- Open work, after the job model of RFC 6:
  1. The SQL adapters and migrations for plans and job logs.
  2. The upload and read routes, with their e2e steps.
  3. The retention settings and the sweep step.
  4. The masked read of the JSON rendering of an OpenTofu plan.

## Open questions

1. **The action for job logs.** The owner named `read plans` and no action
   for job logs. This ADR proposes `unit:read-job-logs`. The other choice is
   `unit:read-plans` for both, because a job log often shows the plan.
2. **Pulumi previews from a workstation.** A `pulumi preview` from a
   workstation records a preview update and its events today, with
   `unit:read`, and the server keeps them for ever. Is the event list of
   such a preview a stored plan, with `unit:read-plans` and the 400-day
   period? A change would make the console need `unit:read-plans` to show
   the events of a preview.
3. **Metadata after expiry.** This ADR keeps the rows of expired items until
   the final delete of the unit. A separate period for the rows is the other
   choice.
4. **Retention of plans that an approval names.** RFC 28 binds an approval to
   a plan. Should the sweep keep a plan that an open approval names, after its
   period ends?
