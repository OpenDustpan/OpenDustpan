# 151. Redaction, and a content key for each history item

Status: accepted · 2026-10-17 · supersedes part of ADR 28, ADR 30, ADR 37 and ADR 41 (see "What this ADR replaces")

## Context

ADR 150 defines history items: the checkpoints of one Pulumi update, one
OpenTofu state version, one stored plan and one job log (ADR 152). A team
sometimes needs one old item gone: a password in a plain attribute, a token
in a job log, a plan with a customer record. The project owner decided the
rules of redaction on 2026-10-17:

- A namespace administrator or a unit administrator can redact old state
  versions, plans and job logs. Nobody can redact the latest state version.
- The server keeps the metadata and the hashes of a redacted item.
- Each redaction writes an audit entry with a reason (ADR 60).
- Redaction uses crypto-shredding with one data key for each version, so
  that a redaction also covers backups.

The facts that the design must meet:

- **Envelope encryption today (ADR 23).** Each scope (a namespace and a unit
  ID) has one random data key. The `Wrapper` port wraps it with the
  key-encryption key. SQL keeps the wrapped key in the key store. Data keys
  never rotate, because the Pulumi CLI keeps ciphertexts of the stack data key
  in the stack config of the user (`unit:encrypt`, `unit:decrypt`).
- **Sealing today (ADR 41 decision 10).** The server seals each OpenTofu state
  blob and the `outputs` column with the data key of the workspace scope. A
  change in review derives a key for each stream from the data key and a
  random salt, and binds the stream to its state version ID. The server does
  not seal Pulumi checkpoint blobs (ADR 37). Their secret values are
  ciphertext under the stack data key. Their other values are plain.
- **Backups (ADR 30, ADR 42).** A backup holds the SQL rows in the portable
  format and copies each blob that SQL names to `blobs/<blob key>` in the
  target. A target holds each blob one time for all of its backups. A backup
  never holds the key-encryption key. Only one backup runs at a time. The
  `backups` table records the target of each backup.
- **Crypto-shredding.** A shredded key makes each copy of its ciphertext
  useless, also copies that the server cannot reach. This is true only when
  no copy of the key survives, and when no other key opens the ciphertext.
  A key derived from the data key of the unit fails this test: each backup
  holds that data key.

## Decision

### The content key

1. **One content key for each history item.** Each history item has its own
   random 256-bit content key. The server never derives it from another key.
   The content key seals all content of its item:
   - Pulumi: every checkpoint blob of one update (each save of ADR 37, with
     every sequence number).
   - OpenTofu: the state blob of one state version, for every upload attempt,
     and its outputs document.
   - A stored plan: each part of the plan (ADR 152).
   - A job log: each chunk of the log (ADR 152).
2. **The content key is in the blob store, not in SQL.** The `Wrapper` wraps
   the content key, as it wraps a data key. The associated data binds the
   wrapped key to its scope, its kind and its item ID. The server writes the
   wrapped key one time, as the blob `content-keys/<content key ID>`. SQL
   keeps a record of each content key: its ID, its scope, its kind, its item
   ID, its wrapper ID, its creation time, and its shred time when it is
   shredded. A content key ID has the prefix `ck-` (ADR 19 scheme).

   There are three reasons for this place:
   - A backup copies the key blob to `blobs/content-keys/<ID>` in its target.
     Every backup in that target shares this one object. One delete in the
     target reaches all of those backups.
   - A dump of the database, a replica, or a write-ahead log archive that the
     operator makes holds no content key. Only the blob store and the backup
     targets hold content keys.
   - A content key never shares a row with data that the restore swap
     replaces (ADR 72 decision 5).
3. **Order of writes.** The server writes the key blob first. It then writes
   the key record in the fenced transaction that creates the item: the
   update, the state version, the plan or the job log. A failed transaction
   leaves an orphan key blob, which the retention sweep deletes. This is the
   rule of ADR 26 and ADR 37 for content blobs.
4. **Open.** To open content, the server reads the key record in SQL first.
   A shredded record answers `secret.ErrShredded` before any blob read. So
   every instance (ADR 72) sees a shred at once. The server may keep wrapped
   key bytes in memory by key ID, only after the record check.
5. **The data key of the unit does not change.** The stack data key of
   ADR 23 stays as it is, for `unit:encrypt`, `unit:decrypt`, the secret
   values in the stack config and the secret values in each checkpoint. The
   Pulumi CLI never sees a content key. A checkpoint is sealed two times: its
   secret values under the stack data key, and the whole blob under the
   content key of its update. The workspace data key of ADR 41 seals no new
   content. It stays for the unit until its final delete.
6. **The Pulumi CLI flows stay the same.**
   - A checkpoint save (`checkpoint`, `checkpointverbatim`) seals the body
     with the content key of the update.
   - A delta upload (`checkpointdelta`, ADR 37) applies to the last save of
     the same update. The server opens that save with the same content key.
   - `stack export` opens the latest checkpoint. `stack export --version N`
     opens the checkpoint of update N.
   - `stack import` and a restore (ADR 28) make a new update. The server
     opens the old checkpoint and seals the copy with the new content key.
   - Each route keeps its request and response shapes.
7. **The OpenTofu flows stay the same.**
   - The declare step of a state version (ADR 26) creates its content key.
     The upload seals the state blob as it streams, as ADR 41 decision 10 does
     now, with the content key in place of the workspace data key. The binding
     to the state version ID stays in the associated data. When the change in
     review that derives a key for each stream lands, each stream key derives
     from the content key. A shred of the content key then also makes every
     stream key unrecoverable.
   - The MD5 and the size of a state version stay those of the plain bytes.
     So the TFE-compatible API answers the same values.
   - `tofu state pull`, the remote state of another workspace, and the outputs
     routes open the current state version, which is never redacted.
   - A restore (ADR 28) opens the old state version and seals the copy with
     the content key of the new state version.
8. **A purge shreds every content key of the unit.** The final delete
   (ADR 47) shreds the content key of each item of the unit, in addition to
   the data key and the blobs. It then deletes the key blobs, in the live
   blob store and in each backup target (decision 14). So a purge also
   reaches backups.
9. **Start-up check.** The wrapper IDs of the content key records count in
   the start-up check of ADR 23 decision 3, as the wrapper IDs of data keys
   do.
10. **No migration.** No release exists, so no installation holds content
    that the server must seal again. The change replaces the sealing of
    ADR 41 decision 10 for new content, and it includes no migration code.

### Redaction

11. **What a redaction is.** A redaction destroys the content of one old
    history item and keeps its metadata. It cannot be undone. It is not
    masking: a masked state read hides secret values and keeps them
    (ADR 41). A redaction shreds the content key of the item and deletes the
    content blobs.
12. **What a redaction can target.**
    - A state item that is not the latest state item (ADR 150 decision 2).
      A Pulumi update that runs, and an OpenTofu state version that is still
      pending, are not targets. The answer is 409.
    - A stored plan, and a job log of a finished job (ADR 152).

    A request for the latest state item answers 409 with an error that says
    that the latest state cannot be redacted. A request for an item that is
    already redacted answers 409 and names the earlier redaction.
13. **The checks and the write, in one fenced transaction.** The server:
    1. checks the target (decision 12),
    2. checks that blame (ADR 150) compared the item and the next state item.
       Otherwise it answers 409, and the client can try again. Without this
       check, blame would lose the changes of the item, because blame reads
       both items,
    3. marks the item redacted in the table of its tool, with the redaction
       ID,
    4. marks the content key record shredded,
    5. writes the redaction record: the ID (prefix `rdx-`), the item, the
       actor (ADR 150 decision 3), the time, the reason, the content key ID,
       the content blob keys, and one shred row for each backup target that
       the `backups` table names,
    6. writes the audit entry (decision 17).

    A redaction is a write in a namespace, so it carries the namespace fence.
14. **The shred operation.** After the commit, a durable operation (ADR 72)
    finishes the redaction:
    1. It deletes the key blob and the content blobs from the live blob
       store.
    2. It waits until no backup that started before the commit still runs.
       Only one backup runs at a time (ADR 30). Without this wait, a backup
       could copy a key blob that it read before the delete into a target
       after the delete.
    3. For each backup target of the redaction, it writes the ledger object
       `redactions/<redaction ID>.json`, then deletes
       `blobs/content-keys/<content key ID>`, then deletes the content blobs
       (`blobs/<blob key>`). The ledger object holds the redaction ID, the
       namespace, the tool, the unit ID, the item kind and ID, the content key
       ID, the blob keys and the time. It holds no reason and no name.
    4. It marks each target done. A target that does not answer stays
       pending. The operation tries again with a back-off and counts the
       failure in a metric.

    The redaction record answers the state of each target, so an operator
    can see which backups the redaction reached.
15. **What a redaction keeps.**
    - Pulumi: the update row with its version, kind, result, times, actor,
      `restoredFrom`, checkpoint size and SHA-256, and its resource count.
    - OpenTofu: the state version row with its ID, lineage, serial, MD5,
      size, times, holder, actor, force flags and `restored-from`, and the
      sensitive marks (paths only, ADR 41 decision 9).
    - A plan or a job log: its row with its size, SHA-256, times and actor.
    - Every resource change row of the item (ADR 150).
    - The redaction record and the audit entry.

    The hashes of a redacted item stay readable. A hash of a whole state
    document does not show a value, but a person who knows every other byte
    of the document could test guesses for one value against it. The
    documents say this (question 6).
16. **Restore and `backup check`.** A backup that a redaction reached has no
    key blob for the item, and maybe no content blobs.
    - `backup check` reads the ledger objects of the target. A missing object
      that a ledger object names is not an error. The check reports the count
      of redacted items.
    - A restore (ADR 38) reads the ledger objects before it loads the staged
      data. For each item that a ledger object names, the restore marks the
      item redacted and the content key shredded in the staged data, and it
      writes the redaction record with the cause `restore`. It copies no blob
      that a ledger object names. A restore from a backup made before a
      redaction so stays redacted.
    - A key blob that is missing with no ledger object stays an error, as
      ADR 30 decision 8 says for a missing blob.
17. **The audit entry.** Each redaction writes one audit entry of the kind
    `change`, with the action `unit:redact`, the unit as the object, the item
    ID as the version, and the reason. The audit entry gets a new optional
    field `reason`, encoded in the hash as `valueCount` is (ADR 60 decision
    5). The server writes the entry in the transaction of decision 13, so a
    failed audit write makes the redaction fail (ADR 60). The end of a
    retention period (ADR 152) writes the same entry, with the principal
    `@server` and the reason `retention period ended`.
18. **The reason.** The reason is required. It is 1 to 1000 characters of
    plain text, with no control characters. Every reader of the audit log of
    the namespace can read it (`namespace:read-audit`), and the redaction
    record answers it to `unit:read`. So the console tells the person to
    describe the cause and not to paste the secret.

### Routes and actions

19. **Routes.** Each tool adapter serves the redaction of its own state
    items (ADR 28 decision 1). The paths are proposals, and the e2e step of
    each route sets the final path:
    - Pulumi: `POST /api/stacks/{namespace}/{project}/{stack}/updates/{version}/redact`
      with `{"reason": "..."}`. The history response gets the field
      `redaction` with `id`, `at`, `by` and `reason`. A read of a redacted
      checkpoint (`export/{version}`, the masked state read, reveal) answers
      410 as `{"code": 410, "message": "..."}`. The message names the
      redaction ID.
    - TFE-compatible API: `POST /api/v2/state-versions/{state_version_id}/actions/redact`
      with a JSON:API document of the type `redactions` and the attribute
      `reason`. The state version gets the attributes `redacted-at`,
      `redacted-by` and `redaction-id`. Its `status` stays `finalized`, and
      its download URLs are null. A download of a redacted state version
      answers 410 as a JSON:API error.
    - Plans and job logs: the routes of ADR 152.
    - Every tool: `GET` of one redaction record by its ID, with the state of
      each backup target, in the inventory API:
      `GET /api/inventory/redactions/{redaction_id}`.
20. **Actions.**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Redact a state item, a plan or a job log | `unit:redact` | unit |
    | Read a redaction record | `unit:read` | unit |
    | Read the history with redactions | `unit:read` | unit |

    `unit:redact` is a new action. The built-in roles of a namespace
    administrator and of a unit administrator get it (ADR 52). No other
    built-in role gets it. Each redaction also passes the policy check
    (ADR 17).

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **ADR 41, decision 10:** "The server seals OpenTofu state blobs and the
  `outputs` column with the data key of the workspace scope." The server now
  seals them with the content key of each state version (decisions 1 and 7).
  "The data key of a workspace goes with the final delete" stays true, and
  the purge also shreds every content key (decision 8).
- **ADR 37, decision 1:** the blob store keeps the content of each checkpoint
  as the CLI sent it. The server now seals each checkpoint blob with the
  content key of its update (decision 6). The blob keys and the SQL metadata
  of ADR 37 stay.
- **ADR 28, decision 3:** a restore "copies the content to a new blob key
  and checks the MD5 while it copies". The restore now opens the old content
  and seals the copy with a new content key. It still checks the MD5 of the
  plain bytes. A redacted state version cannot be restored.
- **ADR 30, decision 8, and ADR 38:** a blob that a backup names and the
  target does not hold stops a restore. A blob that a ledger object names is
  now an expected gap, and the restore marks its item redacted
  (decision 16).

## Relation to the RFCs

- **RFC 33** proposes customer-managed keys and per-namespace keys. A content
  key is wrapped by the `Wrapper` port, as a data key is. So a namespace that
  later gets its own wrapper also wraps its content keys with it, and a
  revoked customer key makes every item of that namespace unreadable. RFC 33
  also asks whether the server or the storage layer encrypts blobs: for
  history content, this ADR decides that the server does.
- **RFC 39 and ADR 41** keep masking as the way to hide secret values in a
  read. Redaction is a separate operation that destroys content. The two
  terms stay apart.
- **RFC 18** keeps the retention of state items open. This ADR adds no
  retention for state items: only a person redacts one.

## Consequences

- A redaction reaches every backup target that the `backups` table names,
  when the target answers. It does not reach:
  - a copy of a target that the operator made outside the server,
  - an older object version in a bucket with object versioning, or an object
    under an object lock,
  - a target that the operator stopped using and that the `backups` table no
    longer names.

  The backup guide says this, and tells the operator to turn off versioning
  for the prefix `blobs/content-keys/` (question 4).
- The live blob store holds the key blobs. An operator who copies or
  replicates the blob store outside the server also copies content keys.
  The documents say this.
- Each open of history content reads one more SQL row and one more blob (the
  key) than today. The server may keep wrapped keys in memory. The release
  load run (ADR 40) measures the cost.
- A rotation of the key-encryption key (the rewrap command of ADR 23) must
  write a new key blob for each content key and delete the old one, in the
  live store and in each target. A target that holds a key wrapped by the old
  key-encryption key stays readable only with that key.
- A Pulumi checkpoint blob is now sealed at rest. This closes the gap that
  ADR 41 left for plain values in Pulumi checkpoints.
- The `secret` package gets a second port, `ContentKeyIndex`, and the type
  `ContentRef`. Both are tool-neutral: the kinds are `state`, `plan` and
  `job-log` for every tool.
- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. `ContentKeyIndex`, its conformance suite (`secrettest.RunContentKeys`)
     and the SQL adapters.
  2. The content sealer, with the key blob and the open check.
  3. Seal Pulumi checkpoints and move OpenTofu sealing to content keys.
  4. The redaction ledger (`history.RedactionLog`), its conformance suite
     (`historytest.RunRedactions`) and the SQL adapters.
  5. The `Redactor` of each tool, then the routes and the audit field
     `reason`.
  6. The shred operation, the ledger objects, `backup check` and the
     restore rule.
  7. Content key shredding in the purge.
  8. E2e steps: two `up` runs, a redaction of version 1, `stack history`,
     `stack export --version 1` (410), `stack export`, and `up`. Two `apply`
     runs, a redaction of the first state version, `tofu state pull`, and an
     `apply`. A backup before the redaction and a restore after it, with the
     item still redacted.

## Open questions

1. **Pulumi update events.** The events of an update can hold values that a
   team wants gone, for example in a diagnostic message. This ADR keeps the
   events of a redacted update. A redaction could also seal the event bodies
   under the content key of the update, and shred them with it. The cost is a
   decrypt for each event read.
2. **TFE backing data actions.** TFE has
   `POST /api/v2/state-versions/{id}/actions/permanently_delete_backing_data`,
   with no request body. go-tfe calls it. Should the TFE-compatible API serve
   it as an alias of the redaction, with a fixed reason? The owner requires a
   reason for each redaction.
3. **Redaction within the recovery window.** ADR 153 keeps the last state
   item that holds a deleted resource as the way to recover the record of
   that resource. Should a redaction of that item, within the recovery
   window, need a confirmation field?
4. **Object versioning on the target.** Should `backup check` and the upgrade
   check (ADR 33) report an S3 target with versioning or an object lock, as a
   target that redaction cannot fully reach?
5. **The reason in the ledger object.** A restore writes the redaction record
   with the cause `restore` and no reason, because the ledger object holds no
   reason. The audit log of the restored installation then has no entry with
   the first reason. Should the ledger object hold the reason?
6. **Hashes of redacted content.** The owner decided to keep the hashes. A
   keyed hash would stop guesses against one value, but the TFE-compatible
   API must answer the plain MD5. Is the risk of decision 15 acceptable as
   documented?
