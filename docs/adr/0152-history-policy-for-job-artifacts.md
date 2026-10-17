# 152. History policy for job artifacts

Status: accepted · 2026-10-17

## Context

A job (ADR 100) makes two kinds of content that the server did not keep
before: the plans of its plan phases, and the logs of its steps. ADR 102
defines how the job model stores them, and ADR 103 defines the actions of
the job API. ADR 150 calls a plan and a version of a job log history items.
ADR 151 gives each history item its own item key, and defines redaction.

The project owner decided on 2026-10-17:

- The server stores plans, behind an action `read plans`. It keeps them for
  400 days. A self-hosted installation can change this period.
- The server keeps job logs for 400 days. A person can redact a job log.
- State readers can read plans.

The owner approved every decision of this ADR on 2026-10-17, after review
round 1 of the pull requests that add this ADR and ADRs 100 to 103. That
review found two designs for plans and job logs, one in each pull request.
This ADR and ADR 102 now split the work, so that one design remains.

## Decision

### The split

1. **The job model owns the artifacts.** ADR 102 and ADR 103 own plan and
   log artifacts: their IDs, their parts, the chunks of a log, the blob
   layout, the upload and read ports, the routes, and the actions
   `job:read-plans` and `job:read-logs`. The job model takes its sealing
   from ADR 151: each plan and each version of a log is one history item,
   with its own item key, of the kind `plan` or `job-log`.
2. **History owns redaction and retention.** The `history` package and
   ADR 151 own the redaction of every item kind: one action
   (`unit:redact`), one `Redactor` port, one `RedactionLog` and one shape of
   audit entry. This ADR owns the retention policy of plans and job logs.
   The job model implements `history.Redactor` for its items, and runs the
   conformance suite `historytest.RunRedactor` with the table of job
   artifacts.

### Retention

3. **The periods.** The server keeps a plan and a version of a job log for
   400 days from the creation of the item. Two settings change the periods
   for an installation (ADR 63): `--plan-retention`
   (`DUSTPAN_PLAN_RETENTION`) and `--job-log-retention`
   (`DUSTPAN_JOB_LOG_RETENTION`). The minimum is one day, so that a typing
   error cannot shred every plan and log at the next sweep. A change of a
   setting writes an audit entry (ADR 63). The hosted distribution sets its
   own periods (ADR 59).
4. **The expiry is stored with each item.** The server computes the expiry
   time when it creates the item, and stores it with the item. A new period
   applies to new items only. So a change of a setting never makes an item
   expire at once. The period starts at the creation of the item, not at the
   end of its job.
5. **The end of the period is a redaction.** The retention sweep finds each
   plan and each log version whose expiry time is in the past. It redacts
   each one through the `Redactor` of the job model, on the path of
   ADR 151 decisions 15 and 16, with the actor `@server`, the cause
   `retention` and the reason `retention period ended`. The audit entry has
   the shape of ADR 151 decision 26, with the action `unit:redact`. The
   metadata stays.
6. **Plans of a job that has not ended.** A plan of a job that has not
   ended does not expire before the end of its job. This covers a plan that
   an open approval names (ADR 103). When the job ends after the expiry
   time, the next sweep redacts the plan.
7. **The metadata stays until the purge.** The rows of expired and
   redacted plans and log versions stay until the final delete of the unit
   (ADR 47). They hold sizes, hashes, times and actors, never content. No
   separate period applies to them.

### Redaction of job artifacts

8. **One action for every redaction.** A person redacts a plan or a log
   version with `unit:redact` on the unit, with a reason (ADR 151). The job
   API serves the route and calls the `Redactor`.
9. **Substitution in logs.** The job model keeps its substitution
   redaction of a log (ADR 102): a person gives text to replace, the server
   writes the next version of the log with each match replaced and a new
   item key, and then redacts the older version through the `Redactor`,
   with the cause `request` and the reason of the person. The current
   version of a log cannot be redacted, and an open version answers 409.
   The audit entry of the substitution never holds the text that the person
   gave.

### Actions and roles

10. **Actions.**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Read the readable plan, masked | `job:read-plans` | unit |
    | Read a plan part that holds plain secret values | `job:read-plans`, `unit:read-secrets` | unit |
    | Read the content of a job log | `job:read-logs` | unit |
    | Redact a plan or a log version | `unit:redact` | unit |
    | Change a retention period | `server:manage-settings` | `@server` |

    ADR 103 defines `job:read-plans` and `job:read-logs`, and the audit entry
    of a read of plain secret values.
11. **State readers read plans.** Every built-in role that has `unit:read`
    also gets `job:read-plans`. A plan shows the values that the next state
    will hold, so a person who can read the state can read the plan. A
    custom role (ADR 52) can still give `unit:read` without
    `job:read-plans`.

### Previews from a workstation

12. **A workstation preview is not a stored plan.** A `pulumi preview` from
    a workstation records a preview update and its events, as now (ADR 20),
    with `unit:read`. It is not a stored plan, and this retention policy
    does not apply to it. ADR 151 seals its event payloads under the item
    key of the preview update. Only a plan phase of a job makes a stored
    plan.

## Relation to the RFCs

- **RFC 28** asks how the server stores a plan and whether a plan is covered
  by the rule on state. ADR 102 stores a plan as blobs with hashes, and this
  ADR gives it a retention period. ADR 151 seals it like state. Plan-bound
  approvals are in ADR 103.
- **RFC 6** proposes runners and jobs. ADRs 100 to 103 define the job
  model, and this ADR adds the history policy of its artifacts.
- **RFC 39** gives the masking rules that the read of the readable plan
  uses.

This ADR supersedes no decision. ADR 102 follows decision 4: the period
starts at the creation of the item.

## Consequences

- Plans and job logs grow the blob store until their retention period ends.
  The sizing guide states the growth for each job.
- The retention sweep writes one audit entry for each expired item.
- The `history` package holds no type and no port for plans or job logs.
  The job model holds them, with the defaults of decision 3.
- Built-in roles change: each role with `unit:read` gets `job:read-plans`.
  The role table of the `identity` package needs this change.
- Open work, with the job model of ADRs 100 to 103:
  1. The `Redactor` of the job model, with `historytest.RunRedactor`.
  2. The retention settings, the expiry time of each item and the sweep
     step.
  3. The role table change of decision 11.

## Answered questions

The owner approved these answers on 2026-10-17. The decisions above apply
them.

1. **The actions for plans and job logs.** `job:read-plans` and
   `job:read-logs` (ADR 103). A redaction uses `unit:redact` (decisions 8
   and 10).
2. **Pulumi previews from a workstation.** They are not stored plans
   (decision 12).
3. **Metadata after expiry.** The metadata stays until the purge
   (decision 7).
4. **Retention of plans that an approval names.** A plan does not expire
   before its job ends (decision 6).
