# 102. Job plans and logs: parts, chunks, uploads and reads

Status: accepted · 2026-10-17. Decisions 2, 7 and 8 superseded in part by ADR 190. Decision 2 extended by ADR 194. Route paths superseded in part by ADR 210

Changed by:

- ADR 190 changes decisions 2, 7 and 8: the plan reader and a stale plan of an
  apply job.
- ADR 194 extends decision 2: a third part kind policy-report.
- ADR 210 changes: the route paths and the server: action names.
- ADR 220 changes decisions 3 and 4: the plan, log and item key blob keys get
  a namespace prefix.
- ADR 299 changes decision 8: the plan comment on a review request holds
  counts and resource addresses by default.

## Context

A job makes two kinds of content: the plan of its plan phase, and the logs
of its steps. Both can hold secret values. An OpenTofu plan file holds the
values that the plan reads. A tool can print a secret to its log.

The project owner decided:

- The server stores and shows plans, with a `read plans` action. The server
  keeps them 400 days by default, and the retention is a setting.
- The server keeps job logs 400 days, and a person can redact them. The
  metadata and the hashes stay. Redaction uses crypto-shredding, with one
  key for each version.

The history model decides the parts of this that are the same for every
history item. ADR 150 calls a plan and a job log history items. ADR 151
gives each history item its own random **item key**, stores the wrapped key
as a blob outside SQL, and defines redaction, with a shred ledger in each
backup target. ADR 152 gives the retention of plans and job logs. The owner
approved this split on 17 October 2026:

- **The job model** owns the plan and log artifacts: their IDs, the parts of
  a plan, the chunks of a log, the blob layout, the upload and read ports,
  the routes, and the actions `job:read-plans` and `job:read-logs`.
- **The history model** owns the item key and its seal, redaction
  (`unit:redact`, `history.Redactor`) and the retention policy, with its
  defaults and the names of its settings.

ADR 152 has the table of the names that this ADR uses. Its names are the
reference.

[RFC 28](../rfc/0028-stored-plans.md) proposes stored plans and approvals
bound to a plan hash.

The owner approved the decisions of this ADR on 17 October 2026.

## Decision

1. **IDs.** A stored plan has an ID with the prefix `pln-`. Each version of
   the log of one step has an ID with the prefix `jlv-`. These are the IDs
   of the items, not of keys. The item key of a plan or of a log version
   has its own ID, with the prefix `ik-` (`secret.ItemKeyID`, ADR 151),
   and the item kind `plan` (`secret.ItemKindPlan`) or `job-log`
   (`secret.ItemKindJobLog`). `job.PlanItem` and `job.LogItem` give the
   `history.ItemRef` of a plan and of a log version. No key has the ID of a
   plan or of a log version, and no plan or log uses a data key of a
   scope.
2. **One plan for each plan phase, with two parts.** A plan phase stores one
   plan, `job.PlanRef`. Each part is one blob with a kind, a size, the
   SHA-256 of the plaintext and a flag `secret`:
   - `plan-display` is the readable JSON plan: `tofu show -json` or
     `pulumi preview --json`. The console shows it.
   - `plan-apply` is the plan that the apply phase reads: an OpenTofu plan
     file, or a Pulumi update plan (`--save-plan`). It has the flag
     `secret` when it holds plain secret values, as an OpenTofu plan file
     does. Only the runner of the apply phase of the same job reads it. No
     person can download it.

   The plan has one state version, the one that it read (decision 6). So
   the two parts can never read different state versions.
3. **Blob layout.** The content is in the blob store, and the database
   holds the metadata: the IDs, the sizes, the hashes, the times and the
   item key ID. The keys are:
   - `job/<job ID>/plan/<plan ID>/<part>` for a part of a plan, with the
     part kind as its name (`job.PlanPartKey`).
   - `job/<job ID>/log/<log version ID>/<chunk number>` for a chunk of a
     log, with the chunk number in 8 digits (`job.LogChunkKey`).
4. **Sealed with one item key for each plan and for each log version.** The
   sealing is the rule of ADR 151. Each plan, and each version of a log, has
   one random item key. It seals every part of the plan, or every chunk of
   the version, and nothing else (`secret.ItemSealer`). The server writes
   the wrapped item key as a blob first, outside SQL
   (`item-keys/<item key ID>`). It then writes the key record in the fenced
   transaction that creates the plan or the log version
   (`secret.ItemKeyIndex`). A redaction shreds the item key and writes the
   shred ledger in each backup target, as for a state item. The job model
   adds no delete of a key to the `secret` package.
5. **Uploads.** An executor uploads through `job.Recorder`, with its phase
   claim:
   - `PutPlan` stores the whole plan in one call, after the last step that
     names an output. It writes the item key and the part blobs first, and
     then the plan in one transaction. A plan does not change after that.
     A second plan for the phase fails with `ErrConflict`. A part has at
     most 256 MiB (`job.MaxPlanPart`).
   - `AppendLog` sends each chunk of the log of a step, with a number that
     starts at 0. Chunk 0 creates the log version, with its item key. A
     retry of a chunk with the same hash changes nothing. A known number
     with another hash fails with `ErrConflict`. A chunk has at most 1 MiB
     (`job.MaxLogChunk`). The server writes each chunk as one blob, so the
     console can show a log while the step runs. `EndStep` closes the log
     version with its chunk count and the hash of its plaintext.
6. **The server sets the state version of a plan.** The claim of a plan
   phase records the current state version of the unit in
   `Phase.StateVersion`, in the transaction of the claim. Each takeover
   records it again. No executor gives it. `PutPlan` copies it to
   `PlanRef.StateVersion`, and checks in its transaction that it is still
   the current state version of the unit. When a state write landed during
   the plan phase, `PutPlan` stores no plan, fails the phase with a reason
   that names both versions, and returns `ErrPlanStale`. So a plan never
   claims a state version that it did not read.
7. **A plan binds approvals, and a stale plan never applies.**
   - `PlanRef.SHA256` of each part is the hash of its plaintext. A gate
     with `BindPlan` binds each approval to the plan: its ID, the two
     hashes and the state version (`job.PlanBinding`). An approval of a
     plan that changed fails with `ErrPlanChanged`.
   - At the dispatch of an apply phase that comes after a plan phase, the
     job store checks the plan in the transaction of the dispatch with
     `job.CheckPlan`. The dispatch fails with `ErrPlanStale` when the plan
     phase stored no plan, when the current state version of the unit is
     not the version that the plan read, when the plan is older than the
     maximum plan age, or when the history model redacted the plan. The
     job then goes back to its plan phase (`replan`), plans again, and asks
     for approval again. The old plan stays as a history item until its
     retention ends.
   - The maximum plan age is an installation setting. The default is 24
     hours (`job.DefaultMaxPlanAge`).
   - The apply phase gets the plan with its hashes, and the runner refuses
     a part with another hash.

   For OpenTofu, the apply step applies the saved plan file. OpenTofu itself
   refuses a saved plan when the state changed after the plan.

   For Pulumi, the binding means two things. First, the apply fails when the
   state version of the stack changed after the preview: the server checks
   it at the dispatch, and again when the Pulumi CLI starts the update of
   the job. Second, the resource changes of the apply must match the
   approved preview: the plan step runs `pulumi preview --save-plan`, and
   the apply step runs `pulumi up --plan` with that file. When a resource
   change is not in that plan, the Pulumi CLI stops the update, and the job
   fails. The Pulumi CLI checks the plan while the update runs, not before
   it starts. So the changes before the stop stay, and the reason of the
   failure tells the operator to read the state of the stack. OpenDustpan
   never lets an update make a change that no person approved.
8. **Reads.** `job.ArtifactStore` opens content: `OpenPlanPart` for a part
   of a plan, and `OpenLog` for a log version, with its chunks in order. A
   read of content whose item key a redaction shredded fails with
   `secret.ErrShredded`. The metadata stays in
   the job record: `PlanRef` on the plan phase, and `LogRef` on each step
   record. The actions:
   - `job:read-plans` on the unit allows a read of the readable plan, with
     the values that the tool marks as sensitive masked, with the masking
     rules of ADR 41 for the tool.
   - An unmasked read of the readable plan also needs `unit:read-secrets`,
     and writes an audit entry of the kind `reveal` before the answer.
   - `job:read-logs` on the unit allows a read of a log. The server does not
     mask a log, because it cannot find secret values in text. The runner
     masks the secrets of the profile before it sends a chunk (ADR 101
     decision 10).
   - The part `plan-apply` has no route for a person. Only the runner of the
     apply phase reads it, through its claim.
   - Each built-in role (ADR 52) that has `unit:read` also gets
     `job:read-plans`. A role can give `job:read` without `job:read-logs`.
9. **Routes.** The routes land with the job API, in the tool-neutral
   OpenDustpan API. The paths are proposals, and the e2e step of each route
   sets the final path:
   - `GET /api/jobs/{job_id}/plan`: the metadata of the plan, with
     `job:read`.
   - `GET /api/jobs/{job_id}/plan/plan-display`: the readable plan, masked,
     with `job:read-plans`. The query `reveal=true` answers it unmasked, with
     `unit:read-secrets` too.
   - `GET /api/jobs/{job_id}/steps/{step}/logs/{log_version_id}`: the
     content of a log version, with `job:read-logs`.
   - `POST /api/jobs/{job_id}/plan/redact`: the redaction of the whole
     plan, with a reason, with `unit:redact` (ADR 152 decision 8).
   - `POST /api/jobs/{job_id}/steps/{step}/logs/{log_version_id}/redact`:
     with a reason, and with text to replace, the redaction by substitution
     of decision 11. With a reason only, the redaction of the whole log
     version. Both need `unit:redact`.

   The runner uploads through the runner protocol (`/runner/v1`), not
   through these routes.
10. **Retention is the history model's.** ADR 152 gives the retention
    policy, and the `history` package holds its values. The job model
    reads them and keeps no retention value of its own:
    - The period is 400 days by default (`history.DefaultRetention`), and
      at least 1 day (`history.MinRetention`). The settings are
      `--plan-retention` (`DUSTPAN_PLAN_RETENTION`) and
      `--job-log-retention` (`DUSTPAN_JOB_LOG_RETENTION`), and
      `history.RetentionSettings.Validate` checks them. The settings belong
      to the history model.
    - The period starts at the creation of the item, not at the end of its
      job. The server stores the end of the period with each plan and each
      log version when it creates it (`ExpiresAt`, with
      `history.ExpiresAt`), so a change of a setting applies to new items
      only.
    - At the end of the period, the retention expiry redacts the item
      through the `history.Redactor` of the job model (decision 11), with
      the actor `@server`, the cause `retention`
      (`history.CauseRetention`) and the reason `retention period ended`.
      This is the same path as a redaction by a person. Retention may
      expire the current version of a log. A plan of a job that has not
      ended does not expire before the end of its job
      (`history.ErrInProgress`), and the next expiry run after the end redacts
      it.
11. **Redaction.** A redaction of a plan or of a log version is the
    redaction of ADR 151, with the action `unit:redact`. The job model
    implements `history.Redactor` for plans and log versions:
    `job.ArtifactStore` embeds it. One fenced transaction marks the item
    redacted, shreds its item key, appends the record to the
    `history.RedactionLog` and writes the audit entry. The rules of the job
    model are `job.CheckPlanRedaction` and `job.CheckLogRedaction`:
    - A plan of a job that has not ended answers `history.ErrInProgress`,
      for every cause.
    - An open log version (the step runs) answers `history.ErrInProgress`,
      for every cause.
    - A request (cause `request`) for the current version of a log answers
      `history.ErrLatest`. The cause `retention` redacts it.
    - An item that is already redacted answers `history.ErrRedacted`, and
      a read of its content `secret.ErrShredded`.

    The reference adapter runs `historytest.RunRedactor` with
    `historytest.JobArtifactSituations()` for plans and
    `historytest.JobLogSituations()` for log versions. The ledger object
    of each redaction is `history.LedgerRecord`, with its `Cause`. After a
    restore, `history.RedactionLog.RequeueShreds` sets the shred targets of
    the live blob store and of the source target to pending again, as for
    every item kind. The job model has no ledger, no shred operation and no
    error of its own for these cases.

    The job model adds one feature on this primitive: **redaction of a log
    by substitution**.
    - A principal gives the text to replace in one log version, and a
      reason. `job.Service.RedactLog` reads that version, replaces each
      match with `[redacted]`, and writes the result as the next version,
      with a new item key (`ArtifactStore.ReplaceLog`). The new version is
      then the current one.
    - It then redacts the older version through `history.Redactor`, with
      the cause `request` and the reason. The history model shreds its item
      key and deletes its chunks, also in the backup targets.
    - A log of a step that runs cannot be redacted
      (`history.ErrInProgress`). A version that is already redacted fails
      with `history.ErrRedacted`. When the redaction of the older version
      fails after the new version exists, a retry only redacts the older
      version.
    - The audit entry is the entry of the redaction of ADR 151, with the
      reason. The job record keeps the number of matches and the hashes of
      the two versions. Neither ever keeps the text that the principal
      gave.

## Consequences

- The plan and log stores of the history model move to the job model. The
  history model keeps the item key, the redaction and the retention, and
  the job model calls them.
- A redaction deletes the item key blob and the content blobs of a plan or
  a log version in the live blob store and in each backup target, and
  writes the ledger object first (ADR 151). A restore applies every
  redaction, so a redacted plan or log version stays redacted.
- A log has one blob for each chunk. A long log makes many small objects. A
  later change may join the chunks of a closed log into one blob.
- Plans and logs add storage for the retention period of each job. An
  operator can make the retention shorter, down to 1 day.
- A state write during a plan phase fails the job. A person starts it
  again. The plan cannot show a state that it did not read.
- OpenDustpan reads the readable plan only to mask it. The tool decides what
  is sensitive. A value that the tool does not mark shows in the readable
  plan. Redaction by substitution is for logs only.

## Open questions

- Does a state write during a plan phase fail the job, as decision 6 says,
  or does the job plan again on its own, as a stale plan at the dispatch
  does?
- Does the server join the chunks of a closed log into one blob, and when?
  The item key of the version would seal the joined blob too.
- Are 1 MiB for a log chunk and 256 MiB for a part of a plan the right
  limits? A large OpenTofu plan file of many resources may need more.
- A plan of a job that has not ended does not expire before the end of its
  job (ADR 152 decision 6). Does the same rule apply to the log of a job
  that waits for days in `awaiting-state`? This ADR follows ADR 152: a log
  version expires at the end of its period.
- Does a namespace or a unit get its own retention, below the installation
  setting? The history model decides it.
