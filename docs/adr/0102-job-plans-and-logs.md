# 102. Job plans and logs: blobs, retention and redaction

Status: accepted · 2026-10-17

## Context

A job makes two kinds of content: the plans of its plan phases, and the
logs of its steps. Both can hold secret values. An OpenTofu plan file holds
the values that the plan reads. A tool can print a secret to its log.

The project owner decided:

- The server stores and shows plans, with a `read plans` action. The server
  keeps them 400 days by default, and the retention is a setting.
- The server keeps job logs 400 days, and a person can redact them. Only old versions are
  redacted. The metadata and the hashes stay. Redaction uses
  crypto-shredding, with one data key for each version.

[RFC 28](../rfc/0028-stored-plans.md) proposes stored plans and approvals
bound to a plan hash. The `blob` port stores large content, and the
`secret` package seals content with a data key for each scope
([ADR 23](0023-secrets-and-the-built-in-key-encryption-key.md)).

The owner approved the decisions of this ADR on 17 October 2026.

## Decision

1. **Content in blobs, metadata in the database.** The content of a plan
   and of each log version is in the blob store. The database holds its
   metadata: the ID, the size, the SHA-256 of the plaintext, the times and
   the wrapped data key. Keys are:
   - `job/<job ID>/plan/<plan ID>` for a plan.
   - `job/<job ID>/log/<log version ID>/<chunk number>` for a log chunk,
     with the chunk number in 8 digits.
2. **One data key for each plan and for each log version.** The server seals
   each blob with the stream format of the `secret` package. The scope is the
   namespace and the plan ID (`pln-…`) or the log version ID (`jlv-…`).
   So one key opens one plan, or one version of one log, and nothing else.
3. **Two outputs of a plan phase.** A step can leave two plan files:
   - `plan-display` is the readable JSON plan: `tofu show -json` or
     `pulumi preview --json`. The console shows it.
   - `plan-apply` is the plan that the apply phase reads, for example an
     OpenTofu plan file. Only the runner of the apply phase of the same job
     reads it. No person can download it.
4. **Reading a plan.** `job:read-plans` on the unit allows a read of the
   readable plan, with the values that the tool marks as sensitive masked.
   An unmasked read also needs `unit:read-secrets`, and writes an audit
   entry of the kind `reveal`.
5. **A plan binds approvals, and a stale plan never applies.**
   - `PlanRef.SHA256` is the hash of the plan plaintext. `PlanRef.StateVersion`
     is the state version of the unit that the plan read. The job stores
     both plans of the phase, `plan-display` and `plan-apply`, as a
     `job.PlanSet`.
   - A gate with `BindPlan` binds each approval to both plans: the two
     hashes and the state version (`job.PlanBinding`). An approval of plans
     that changed fails with `ErrPlanChanged`.
   - At the dispatch of the apply phase, the job store checks the plans in
     the transaction of the dispatch with `job.CheckPlan`. When the current
     state version of the unit is not the version that the plans read, or
     the plans are older than the maximum plan age, the dispatch fails with
     `ErrPlanStale`. The job then goes back to its plan phase (`replan`),
     plans again, and asks for approval again. The old plans stay for the
     retention.
   - The maximum plan age is an installation setting. The default is 24
     hours (`job.DefaultMaxPlanAge`).
   - The apply phase gets the plans with their hashes, and the runner
     refuses a plan with another hash.

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
6. **Logs stream in chunks.** A runner sends each chunk with
   `Recorder.AppendLog`, with a number that starts at 0. A retry of a chunk
   with the same hash changes nothing. A known number with another hash
   fails with `ErrConflict`. The server writes each chunk as one blob, so the
   console can show a log while the step runs. When the step ends, the log
   version closes with its chunk count and the hash of its plaintext.
7. **Retention of 400 days, as two settings.** The content of plans and of
   log versions stays for 400 days after the end of the job. Two
   installation settings change it: one for plans, one for logs
   ([ADR 63](0063-installation-settings-file-and-change-log.md)). The lowest
   value is 1 day, so a typing error cannot shred every plan at the next
   sweep. The `job.retention` schedule shreds the content after the
   retention with `ArtifactStore.ShredExpired`, and then deletes the blobs.
   That method shreds every version of a log, the current one too. The
   metadata and the hash stay.
8. **Redaction writes a new version and shreds the old one.** A principal
   with `job:redact-logs` gives text to replace. The server reads the current
   version, replaces each match with `[redacted]`, and writes the next
   version with a new data key. Then it shreds the older version and deletes
   its blobs. A redaction cannot shred the current version
   (`ErrCurrentVersion`). This error is for redaction only. A log of a step that runs cannot be redacted
   (`ErrLogOpen`). The audit entry holds the number of matches and the
   hashes of the two versions. It never holds the text that the principal
   gave.
9. **A shred destroys the data key.** A shred deletes the wrapped data key
   and records the time. After that, the content cannot be read, also from a
   copy of the blob store. A read of shredded content fails with
   `ErrShredded`. The ID, the size, the hash and the times stay, so the job
   still shows what existed and when.

## Consequences

- The `secret.KeyStore` port has no delete today. Crypto-shredding needs a
  delete of one wrapped key, with conformance cases. That change lands with
  the first `ArtifactStore` adapter.
- A backup taken before a shred still holds the wrapped key. The shred is
  complete only when the last such backup expires. The backup guide must say
  this, and the backup retention bounds the time.
- A log has one blob for each chunk. A long log makes many small objects. A
  later change may join the chunks of a closed log into one blob.
- Plans and logs add storage for 400 days per job. An operator can make the
  retention shorter.
- OpenDustpan reads the readable plan only to mask it. The tool decides what
  is sensitive. A value that the tool does not mark shows in the readable
  plan. Redaction is for logs only in this ADR.

## Open questions

- Can a plan be redacted as a log can? A plan has one version, so a
  redaction would shred the only version and keep a redacted copy.
- Does the retention count from the end of the job or from the creation of
  the content? This ADR counts from the end of the job.
- Do a namespace and a unit get their own retention, below the installation
  setting?
- Does the server join the chunks of a closed log into one blob, and when?
- Who can read the logs of a job that a runner of another namespace ran?
  Today a pool belongs to one namespace, so this cannot happen.
