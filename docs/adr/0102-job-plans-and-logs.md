# 102. Job plans and logs: parts, chunks, uploads and reads

Status: accepted · 2026-10-17

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
  (`unit:redact`) and the retention policy.

[RFC 28](../rfc/0028-stored-plans.md) proposes stored plans and approvals
bound to a plan hash.

The owner approved the decisions of this ADR on 17 October 2026.

## Decision

1. **IDs.** A stored plan has an ID with the prefix `pln-`. Each version of
   the log of one step has an ID with the prefix `jlv-`. The item key of a
   plan or of a log version has its own ID, with the prefix `ik-`
   (ADR 151). No key has the ID of a plan or of a log version, and no plan
   or log uses a data key of a scope.
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
   the version, and nothing else. The server writes the wrapped item key as
   a blob first, outside SQL. It then writes the key record in the fenced
   transaction that creates the plan or the log version. A redaction shreds
   the item key and writes the shred ledger in each backup target, as for a
   state item. The job model adds no delete of a key to the `secret`
   package.
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
   read of redacted content fails with `ErrRedacted`. The metadata stays in
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
   - `POST /api/jobs/{job_id}/steps/{step}/logs/{log_version_id}/redact`:
     the redaction of decision 11, with `unit:redact`.
   - A redaction of a whole plan or of a whole log version is the redaction
     route of ADR 152.

   The runner uploads through the runner protocol (`/runner/v1`), not
   through these routes.
10. **Retention is the history model's.** The history model gives the
    retention of plans and of job logs, with its settings (ADR 152): 400
    days by default, at least 1 day, counted from the creation of the item.
    The server stores the end of the retention with each plan and each log
    version (`ExpiresAt`) when it creates it, so a change of a setting
    never makes an item expire at once. At the end, the history model
    redacts the item with the cause `retention`, through the same path as a
    redaction by a person. The job model has no retention sweep and no
    schedule for it.
11. **Redaction.** A redaction of a plan or of a log version is the
    redaction of ADR 151, with the action `unit:redact`. The artifact
    store implements the redactor port of the history model for plans and
    log versions. The job model adds one feature on that primitive:
    **redaction of a log by substitution**.
    - A principal gives the text to replace in one log version, and a
      reason. `job.Service.RedactLog` reads that version, replaces each
      match with `[redacted]`, and writes the result as the next version,
      with a new item key (`ArtifactStore.ReplaceLog`). The new version is
      then the current one.
    - It then redacts the older version through the history model, with
      the reason. The history model shreds its item key and deletes its
      chunks, also in the backup targets.
    - A log of a step that runs cannot be redacted (`ErrLogOpen`). A
      version that is already redacted fails with `ErrRedacted`. When the
      redaction of the older version fails after the new version exists, a
      retry only redacts the older version.
    - The audit entry is the entry of the redaction of ADR 151, with the
      reason. The job record keeps the number of matches and the hashes of
      the two versions. It never keeps the text that the principal gave.

## Consequences

- The plan and log stores of the history model move to the job model. The
  history model keeps the item key, the redaction and the retention, and
  the job model calls them.
- A backup taken before a redaction holds the item key blob. The shred
  ledger of ADR 151 reaches each backup target, so a restore from such a
  backup keeps the item redacted.
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
- Does the history model keep a plan of a job that has not ended until the
  job ends, also after its retention? ADR 152 says so for plans. The same
  question applies to the log of a job that waits for days in
  `awaiting-state`.
- Does a namespace or a unit get its own retention, below the installation
  setting? The history model decides it.
