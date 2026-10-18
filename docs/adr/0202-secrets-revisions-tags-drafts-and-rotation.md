# 202. Secrets, revisions, tags, change approvals and rotation of profiles

Status: accepted · 2026-10-18 · part of ADR 200

## Context

The parity list of the project owner (ADR 200) holds, from Pulumi ESC:
static secrets, versioning and tags, approvals where ESC has them, and
rotators. The research note gives the ESC features one by one:

- **Secrets.** `fn::secret` with plain text is sealed at save, and the
  stored document holds the ciphertext. The permission `open` decrypts it.
- **Versions.** Each save makes an immutable numbered revision. The tag
  `latest` always names the newest one. Other tags are named pointers that
  a person moves. An environment also has key and value tags for search.
  ESC has rollback, retract (a revision that nobody can open, whose tags
  move) and a diff of two revisions or of two environments.
- **Approvals.** A ruleset gates updates or opens. With a gate on updates,
  a person writes a draft, which is a change request, and others approve
  it. A draft that is behind the newest revision gets a three-way merge. A
  stack can preview with a draft. A gate on opens gives access for a time
  after approval.
- **Rotation.** A rotator keeps state in the document. A rotation makes a
  new revision, and keeps a current and a previous credential. Each
  environment has one schedule, at most once a day. An environment with a
  rotator cannot roll back in practice, because the rotation retired the old
  credential.
- **Lifecycle.** Clone (also the only rename of ESC), soft delete with a
  restore, deletion protection, and a list of the environments and stacks
  that import an environment.

## Decision

1. **Static secrets.** The save seals each `$secret` with the data key of
   the scope of the profile, `<namespace>` and `profile:<profile ID>`
   (ADR 23), and stores `$sealed` with the ciphertext in its place. The
   plain text reaches no table, no blob, no log and no audit entry.
   - A read of a document with `profile:read` answers the text with the
     `$sealed` values. A person can send them back in a save. The scope
     binds each ciphertext to its profile, so a `$sealed` value that a
     person copies into another profile fails the save with
     `profile.ErrInvalid`.
   - A read with `showSecrets` answers each static secret in plain text. It
     needs `profile:open`, as an open does, and the open rule of the profile
     applies to it (decision 7). It writes an audit entry.
   - The final delete of a profile deletes its data key in the same
     transaction (ADR 23 decision 5).
2. **Revisions.** Each save makes a new revision: the number (1, 2, 3 and
   so on), the document, the principal, the time, an optional reason of at
   most 500 characters, the Expr version and the SHA-256 of the document.
   A revision never changes. A save names the revision that it read, and a
   save on a stale read fails with `profile.ErrConflict`.
   - **A routing change needs `profile:open`.** A **routing change** is a
     change that can change where a block sends secrets, an issued token or
     the result of a login block. The server decides it from the syntax of
     the two documents, with no provider call and no open. The **taint
     set** of a block is each path that an input of the block reads,
     directly or transitively through expressions, in the own tree of the
     profile that holds the block (ADR 201 decision 4), also through its
     imports. These changes are routing changes:
     - any difference in the text of a block of `providers` or
       `rotators`, also a new or a deleted block.
     - any difference in the text of a path of the taint set of a block,
       also when the path holds a plain value (for example a value that
       feeds `with.url`, `address`, `tokenUrl` or `server`).
     - any difference in `imports` or `exports`.

     A rollback, a tag move and the apply of a draft compare the document
     before and after with the same rule. For an import, the server
     compares the document of the imported revision before and after.

     A save, a rollback, a tag move or the apply of an approved draft that
     makes a routing change needs `profile:open` on the profile as well as
     `profile:write`. Without it, the request fails with
     `identity.ErrDenied`, which names the block or the import. So a person
     who can write a profile but not open it cannot point a block at a host
     of their own and receive the secrets (ADR 208 decision 2). A change
     that no taint set holds, and a change of labels, needs
     `profile:write` only.
   - **Taint follows the referrers.** A change to profile Q can also
     change a block of a profile that reads Q. So a save, a rollback, a tag
     move and the apply of a draft on Q compute the taint over each
     referrer of Q (decision 9) that follows the changed revision: each
     profile that imports Q or reads it with `profiles[...]`, at each depth,
     with no tag, with `latest`, or with the tag that moved. A save of Q
     moves `latest`, so it is a tag move for each referrer that names Q
     with no tag. The request needs `profile:open` on each referrer that
     has a block whose taint set holds a changed path of Q. Without it, the
     request fails with `identity.ErrDenied`, which names the referrer, the
     block and the path. A referrer that pins Q with `@<number>`, or with a
     tag that did not move, is not affected. An attachment is not a
     referrer for this rule, because each block runs in the own tree of its
     own profile (ADR 201 decision 3).
   - Each revision is a history item with its own item key (ADR 151). A
     person with `profile:redact` can redact a revision, for example after
     a secret went into a plain value by mistake. A redacted revision
     cannot be opened, and its tags move as decision 5 says.
   - A profile keeps every revision until its final delete.
3. **Tags.** A **revision tag** names one revision of one profile.
   - `latest` always names the newest revision that is not retracted.
     Nobody moves or deletes it.
   - A profile has at most 32 other tags. A name has 1 to 63 characters of
     `a-z`, `0-9`, `.`, `_` and `-`, and starts with a letter, so a tag is
     never read as a revision number.
   - A person with `profile:write` creates, moves and deletes a tag. Each
     change writes an audit entry, with the revision before and after.
     A tag move that is a routing change also needs `profile:open`
     (decision 2), also when only an import of another profile names the
     tag.
     When the change rule of the profile covers tags (decision 6), a move
     of a tag is a draft too.
   - Imports, the run command, the stack config of the Pulumi CLI and the
     provider of OpenTofu name a revision as `name@tag` or `name@number`.
4. **Labels.** A profile has 0 to 32 labels, each a key and a value, for
   views, filters and search (the label of ADR 51 decision 7). A label
   grants nothing. `GET /api/dustpan/v1/namespaces/{namespace}/profile-labels`
   answers each key with its values.
5. **Rollback, retract and diff.**
   - **Rollback** saves the document of an earlier revision as a new
     revision. It is a save, so the rules of decision 2 apply, and under a
     change rule it is a draft (decision 6). The state of each rotator stays the current state (decision
     8), so a rollback never brings back a retired credential. This is the
     case where ESC cannot roll back.
   - **Retract** marks a revision that nobody can open or import. Its tags
     move to the replacement that the request names, or to the newest
     earlier revision that is not retracted. `latest` is never retracted,
     so to retract the newest revision, a person rolls back first. A
     retract needs a reason, and writes an audit entry.
   - **Diff** compares two revisions of one profile or of two profiles of
     one namespace. It answers the change of the text and, for each path of
     a static secret, `changed` or `unchanged`, with no value. With
     `showSecrets` and `profile:open`, it also gives the values. The diff
     also names each block whose `use`, `login` or destination host
     changed, so that a reviewer sees where secrets go (ADR 208). It
     compares the evaluated hosts of the two revisions, not the text, so
     a host that an expression or an import makes is also shown.
6. **The change rule.** A profile can have one change rule. Without one, a
   person with `profile:write` saves directly. With one, a save becomes a
   **profile draft**, and a revision exists only after approval:
   - The rule names the number of approvals (1 to 5), the eligible
     approvers (principals and groups that hold `profile:approve`), and
     whether it covers tag moves.
   - The author of a draft never approves it, and the approvals come from
     distinct people, as for a change order (ADR 192). An agent never
     approves.
   - Each principal has at most one open draft for each profile, and many
     principals can have drafts at the same time.
   - A draft names its base revision. An edit of a draft voids its
     approvals. When the base is not the newest revision, the author updates
     the draft with a three-way merge of the text. A conflict names each
     line, and the author resolves it before a submit.
   - An approved draft applies as a new revision at `POST
     .../drafts/{draft}/apply`, by its author or by a principal with
     `profile:write`. The apply runs the routing checks of decision 2
     again, for the **applier**, on the newest revision and on the
     referrers of that time: the applier needs `profile:open` for each
     routing change. The approvals do not replace this check, because the
     base and the referrers can change after the review. A draft that sees
     no write for 30 days expires.
   - A draft can be opened before approval for a plan, as
     `name@draft:<draft ID>`, by its author and its approvers, each with
     `profile:open` on the profile. Such an open
     has `access` `read` or `open` and never `write` (ADR 205 decision 4), so
     an apply never runs with a draft.
   - A change of the change rule or of the open rule (decision 7) needs
     `profile:manage` and an approval under the change rule as it was
     before the change. When the profile has no change rule, a change or a
     delete of the open rule needs one approval by an eligible approver of
     the open rule as it was, who is not the requester. So a person with
     `profile:manage` alone cannot delete an open rule and go around the
     access requests. A new change rule on a profile that has none needs
     no approval, because it adds a control.
7. **The gate on opens.** A profile can also have an **open rule**: an
   open needs an approved **access request** first.
   - A person asks with a reason, the time that the access lasts (at most 7
     days) and the time that the request stays valid before its first use.
     Approvers approve as in decision 6. The access starts at the first
     open after approval.
   - An open of a profile that imports a profile with an open rule needs an
     access request for each such profile. The console makes them in one
     step, and each one gets its own approval.
   - For a job, ADR 203 decision 5 says how the open rule applies.
8. **Rotation.** A `rotators` block names a rotator (ADR 207 decision 8),
   its inputs, a login block when the rotator needs one, and a schedule:

   ```yaml
   rotators:
     db:
       use: postgres
       with:
         host: db.payments.internal
         managingUser:
           username: rotator
           password: { $secret: "<password of the managing user>" }
         users: [app_blue, app_green]
       schedule: "0 3 * * 1"      # each Monday at 03:00 in the time zone of the namespace
   values:
     db:
       user: ${ rotators.db.current.username }
       password: ${ rotators.db.current.password }
   ```

   - The state of a rotator is not in the document. The store keeps it in a
     **rotation record**, sealed with the data key of the profile. So the
     document stays a text that a person writes, and an open always reads
     the current credential, also with an older revision of the document.
     The outputs are `current` and `previous`.
   - The record is bound to the block name and to the SHA-256 of the
     evaluated target inputs of the block: the rotator name and the inputs
     that name the target, for example `host` and `users`. When a revision
     renames the block or changes its target, the hash changes, and the
     server starts a new record. It never uses the old record for the new
     target. The old record stays for 30 days, so that a rollback finds it,
     and then the server deletes it.
   - **Static target inputs.** The spec of a rotator marks its target
     inputs. A target input is a literal, or an expression that reads only
     plain values of the own tree of the profile. A target input that
     reads `context`, a provider output, a secret or another profile fails
     the save with `profile.ErrInvalid`, which names the input. So one
     block has one record, whatever the context of the open.
   - **A record whose target changed.** The old record stays for 30 days,
     so that a rollback finds it. Its credentials stay live at the remote
     for that time, and `GET .../rotations` lists the record as
     `detached`. At the end of the 30 days, the server calls `Retire` for
     each credential of the record, and deletes the record only when each
     `Retire` succeeded. A record whose `Retire` fails stays, as
     `orphaned`, and the server sends `profile.rotation-failed` each day
     until a person retires the credential at the remote and deletes the
     record with `profile:rotate`. The docs say this.
   - A schedule is a cron expression with at most one run a day, or one time
     (`once`). Each block has its own schedule. A change of the schedule is
     a save. The schedule `profile.rotate` starts one durable operation for
     each due rotation, with the start key `<profile ID>/<block>/<time>`.
   - **One rotation at a time for each block.** A rotation, from the
     schedule or from `POST .../rotate`, first takes the **rotation lease**
     of the block in the store, and gets a lease token. While another
     rotation holds the lease, a manual rotation fails with
     `profile.ErrConflict`, and a scheduled rotation waits for the next
     run. The lease ends when the rotation records its outcome, or after
     90 minutes. The retries of one rotation stop after 60 minutes, and one
     call takes at most 30 seconds (ADR 206 decision 3), so the lease is
     longer than the retries and one more call. Also, `Store.Rotated`
     accepts only the token of the live lease: a rotation whose lease ended
     records nothing and fails with `profile.ErrConflict`.
   - **Slots.** The spec of a rotator declares its slots: the number of
     credentials that the remote can hold at the same time for one block,
     for example 2 for `aws-iam` (two access keys), and 2 for `postgres`
     and `mysql` (two users in turn). The record names the slot of each
     credential. A rotation writes its new credential into one slot that holds no `current` credential. This is a free slot, else the slot of the oldest `retiring` credential, else the slot of `previous`. The credential in that slot must be past its grace period (below). The rotation then clears it from the outputs in the commit that writes `pending`. It calls `Retire` for it before `Rotate`. When the grace period did not end, the rotation fails with `profile.ErrConflict`. The error names the time when the grace period of that credential ends and the slot frees. A scheduled rotation records
     the conflict and runs again at that time. When the grace period of the
     credential in the slot ended, the rotation calls `Retire` for it
     first. A rotator that holds no remote credential, for example
     `password`, declares no limit.
   - **Pending before `Rotate`.** With the lease, the rotation writes the
     state `pending`, a new `rotationId` and the target slot to the record,
     and commits, before it calls `Rotate`. When the server makes the new
     credential itself, for example the password of `password` or the new
     password of a `postgres` user, it makes it and seals it into the
     `pending` record in the same commit. `Rotate` gets the `rotationId` as
     its idempotency key (ADR 206, and ADR 208 for an external adapter). A
     retry after a timeout or a crash reads the `pending` state and calls
     `Rotate` again with the same `rotationId` and the same sealed
     credential, so the remote gives back the same new credential and
     makes no second one.
   - On success, the old `previous` credential, when it still holds a slot, becomes `retiring`, the old
     `current` becomes `previous`, the new credential becomes `current`,
     and the `pending` state ends, in one transaction of the record.
   - **A failure keeps `pending`.** A failure keeps `current` and
     `previous` as they were, and retries with the backoff of the
     operation for at most 60 minutes. Then the rotation records the
     failure and ends its lease, but the record stays `pending` with the
     same `rotationId`. The reason: the last call possibly succeeded at the
     remote, and only its answer did not arrive. The server sends
     `profile.rotation-failed` each day while the record is `pending`. The
     next rotation of the block, from the schedule or from `POST
     .../rotate`, takes the lease and calls `Rotate` again with the same
     `rotationId`, so a credential that the remote made is used and not
     left as an orphan in a slot. `POST .../rotators/{block}/abandon`, with `profile:rotate`, takes the rotation lease, calls `Retire` for the `pending` `rotationId`, and ends the `pending` state only when `Retire` succeeds. While another rotation holds the lease, it fails with `profile.ErrConflict`.
   - `Retire` runs only after a grace period from the time the retired
     credential stopped being `current`. The grace period ends when both
     are true: 12 hours passed, which is the longest open session (ADR 203
     decision 3), and each job that fixed the profile before that time is
     terminal. A runner job ends at the latest at its deadline of ADR 134
     decision 9: the sum of the timeouts of its steps (at most 24 hours
     each, ADR 101 decision 7), their grace periods, and the fixed
     allowances. Until then, the record keeps the credential as
     `retiring`, and a later schedule run retires it. So a running job or
     a live open session that holds it does not fail. The docs say that a
     credential can stay valid for this grace period after the rotation
     that replaced it.
   - Each block rotates alone, so one failure does not stop the others.
   - `POST .../rotate` rotates now, for all blocks or for named blocks.
     It needs `profile:rotate`.
   - A rotation opens its login block with the action `rotate` and the
     access `rotate` (ADR 205 decision 4). The history of rotations of each block keeps
     the time, the outcome, who or which schedule started it, and the class
     of the error, never a credential.
9. **Lifecycle.**
   - **Create** needs `profile:write` on the namespace. A profile has an ID
     with the prefix `prf-` and a name: 1 or 2 segments of `a-z`, `0-9`,
     `.`, `_` and `-`, joined with `/`, at most 100 characters, for example
     `aws-prod` or `payments/aws-prod`. The first segment of a 2-segment
     name is a **profile folder**, which groups profiles in views. A name
     never starts with `prf-`, so a path segment is never both a name and
     an ID (ADR 203 decision 12). The
     Pulumi CLI sees the folder as the ESC project (ADR 209 decision 5).
   - **Rename** keeps the ID. It fails with `profile.ErrInUse` while a
     profile, a blueprint or an attachment names the profile by its name.
     The error names each one.
   - **Clone** copies the newest revision to a new name, and optionally the
     history, the tags and the labels. It needs `profile:open` on the
     source. It always copies the open rule, so a clone never opens a way
     around an access request. It never copies grants or the change rule.
   - **Delete** is a soft delete with a recovery window of 30 days, as for a
     unit (ADR 47, ADR 153). It fails with `profile.ErrInUse` while a
     profile imports it, an attachment names it, or a job that is not
     terminal reads it. **Deletion protection**, a setting of the profile,
     makes each delete fail until a person with `profile:manage` turns it
     off.
   - **Restore** brings back a deleted profile within the window.
   - **Referrers** lists the profiles that import the profile or read it
     with `profiles[...]`, the attachments, the blueprints, and the units
     whose runs read it in the last 90 days, each with the revision.
10. **Fences, backups and limits.** Each write carries the fence of the
    namespace and appends its audit entries in the transaction. The
    profiles, revisions, tags, labels, rules, drafts, access requests,
    rotation records and attachments are namespace data, so the backup of
    the namespace holds them. A namespace has at most 1,000 profiles. A
    write above a limit fails with `profile.ErrLimit`.

## Consequences

- A team without approvals saves in one step. A team with approvals gets
  drafts, reviews and a gate on opens, with the review rules that it
  already knows from change orders.
- A rollback is always safe for a profile with rotators.
- A pin of a revision pins the document, never a rotated credential. The
  docs say so.
- `profiletest` gets cases for each rule: the conflict of a stale save, the
  scope of a sealed value, the moves of tags at a retract, the voided
  approvals at an edit, the merge, the expiry of a draft, the record of a
  rotation and its failure, the refusal of a rename or a delete in use, and
  the fence. It also gets cases for the taint over referrers (a save of an
  import with no tag, a pinned import that is not affected), the routing
  check of the applier of a draft, the open-rule approval with no change
  rule, a rotation into a slot that a `retiring` credential holds, a
  lease token after its lease ended, a `pending` record after the retries,
  and a target input that reads `context`.
