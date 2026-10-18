# 151. Redaction, and an item key for each history item

Status: accepted · 2026-10-17. Supersedes part of ADRs 20, 28, 30, 37, 38, 41 and 43 (see "What this ADR replaces"). Decisions 3, 8, 15, 28 and 29 and the item kinds superseded in part by ADR 154. Route paths superseded in part by ADR 210. Decision 2 superseded in part by ADR 217: the namespace key wraps each item key, and a rewrap writes the key blob again

## Context

ADR 150 defines history items: one Pulumi update, one OpenTofu state
version, one stored plan and one version of a job log. The job model
(ADR 102) owns plans and job logs. A team sometimes needs one old item
gone: a password in a plain attribute, a token in a job log, a plan with a
customer record. The project owner decided the rules of redaction on
2026-10-17:

- A namespace administrator or a unit administrator can redact old state
  versions, plans and job logs. Nobody can redact the latest state version.
- The server keeps the metadata and the hashes of a redacted item.
- Each redaction writes an audit entry with a reason (ADR 60).
- Redaction uses crypto-shredding with one data key for each version, so
  that a redaction also covers backups.

The owner approved every decision of this ADR on 2026-10-17, after review
rounds 1 and 2 of the pull request that adds it.

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
- **Pulumi events and journal entries.** The server keeps the body of each
  engine event of an update as the CLI sent it, in the column
  `update_events.payload` (ADR 20, which accepts the update cycle of
  RFC 3). With journaling (ADR 43 decision 6.1), the server keeps each
  journal batch as a write-once blob, as the CLI sent it. An event message
  or a journal entry can hold a value that a team wants gone.
- **Backups (ADR 30, ADR 42).** A backup holds the SQL rows in the portable
  format and copies each blob that SQL names to `blobs/<blob key>` in the
  target. A target holds each blob one time for all of its backups. A backup
  never holds the key-encryption key. Only one backup runs at a time. The
  `backups` table records the target of each backup.
- **Restore (ADR 38, ADR 72).** A restore loads the backup into a staging
  area, checks it, and swaps it with the live data. The control tables of
  ADR 72 decision 5 stay outside the swap.
- **Crypto-shredding.** A shredded key makes each copy of its ciphertext
  useless, also copies that the server cannot reach. This is true only when
  no copy of the key survives, and when no other key opens the ciphertext.
  A key derived from the data key of the unit fails this test: each backup
  holds that data key.

## Decision

### The item key

1. **One item key for each history item.** Each history item has its own
   random 256-bit item key. The server never derives it from another key.
   The item key seals all content of its item:
   - A Pulumi update: every checkpoint blob of the update (each save of
     ADR 37, with every sequence number), the payload of each event of the
     update, and each journal entry blob of the update (ADR 43).
   - An OpenTofu state version: the state blob, for every upload attempt,
     and its outputs document.
   - A stored plan: each part of the plan. The job model (ADR 102) names the
     parts and takes its sealing from this ADR.
   - A version of a job log: each chunk of that version (ADR 102).
2. **The item key is in the blob store, not in SQL.** The `Wrapper` wraps
   the item key, as it wraps a data key. The associated data binds the
   wrapped key to its scope, its kind and its item ID. The server writes the
   wrapped key one time, as the blob `item-keys/<item key ID>`. SQL keeps a
   record of each item key: its ID, its scope, its kind, its item ID, its
   wrapper ID, its creation time, and its shred time when it is shredded.
   An item key ID has the prefix `ik-` (ADR 19 scheme).

   There are three reasons for this place:
   - A backup copies the key blob to `blobs/item-keys/<ID>` in its target.
     Every backup in that target shares this one object. One delete in the
     target reaches all of those backups.
   - A dump of the database, a replica, or a write-ahead log archive that the
     operator makes holds no item key. Only the blob store and the backup
     targets hold item keys.
   - An item key never shares a row with data that the restore swap
     replaces (ADR 72 decision 5).
3. **Order of writes, in one transaction.** The server writes the key blob
   first. It then writes the key record in the fenced transaction that
   creates the item: the update, the state version, the plan or the log
   version. One SQL adapter implements the `ItemKeyIndex` port and the
   tables of each tool and of the job model, so the key record and the item
   row commit in one transaction. The same is true for the shred and the
   redaction (decision 15). A failed transaction leaves an orphan key blob.
   The orphan cleanup deletes it after a grace period (decision 22).
4. **Open.** To open content, the server reads the key record in SQL first.
   A shredded record answers `secret.ErrShredded` before any blob read. So
   every instance (ADR 72) sees a shred at once. The server may keep wrapped
   key bytes in memory by key ID, only after the record check.
5. **The data key of the unit does not change.** The stack data key of
   ADR 23 stays as it is, for `unit:encrypt`, `unit:decrypt`, the secret
   values in the stack config and the secret values in each checkpoint. The
   Pulumi CLI never sees an item key. A checkpoint is sealed two times: its
   secret values under the stack data key, and the whole blob under the
   item key of its update. The workspace data key of ADR 41 seals no new
   content. It stays for the unit until its final delete.
6. **The Pulumi CLI flows stay the same.**
   - The start of an update creates its item key. A preview also gets an
     item key for its events. A preview is not a history item (ADR 20,
     point 3), so redaction does not target it.
   - A checkpoint save (`checkpoint`, `checkpointverbatim`) seals the body
     with the item key of the update.
   - A delta upload (`checkpointdelta`, ADR 37) applies to the last save of
     the same update. The server opens that save with the same item key.
   - An event batch seals the payload of each event with the item key of
     the update, before the row write. A read of the events opens each
     payload. The cost is one decrypt for each event that a read answers.
   - A journal batch (ADR 43) seals the batch blob with the item key of the
     update. The arrival order, the sequence IDs and the batch SHA-256 of
     the plain bytes stay in the row. Replay opens each batch blob, then
     applies the entries as ADR 43 says. Replay still decrypts no secret
     value of the stack (ADR 43 decision 4.4).
   - `stack export` opens the latest checkpoint. `stack export --version N`
     opens the checkpoint of update N.
   - `stack import` and a restore (ADR 28) make a new update. The server
     opens the old checkpoint and seals the copy with the new item key.
   - Each route keeps its request and response shapes.
7. **The OpenTofu flows stay the same.**
   - The declare step of a state version (ADR 26) creates its item key.
     The upload seals the state blob as it streams, as ADR 41 decision 10 does
     now, with the item key in place of the workspace data key. The binding
     to the state version ID stays in the associated data. When the change in
     review that derives a key for each stream lands, each stream key derives
     from the item key. A shred of the item key then also makes every
     stream key unrecoverable.
   - The MD5 and the size of a state version stay those of the plain bytes.
     So the TFE-compatible API answers the same values.
   - `tofu state pull`, the remote state of another workspace, and the outputs
     routes open the current state version, which is never redacted.
   - A restore (ADR 28) opens the old state version and seals the copy with
     the item key of the new state version.
8. **A purge shreds every item key of the unit, and writes a tombstone.**
   The final delete (ADR 47) shreds the item key of each item of the unit,
   in addition to the data key and the blobs. In the same fenced
   transaction, it writes one tombstone for the unit (`history.Tombstone`,
   ID prefix `tmb-`), with the cause `purge`. The tombstone is a durable
   shred record in a control table (decision 20). It names the unit, every
   item key of the unit, the blob key prefixes that hold the content of the
   unit, the actor, the time and the hash of the audit entry of the purge,
   and one shred target for the live blob store and for each backup target
   that the `backups` table names. The shred operation (decision 16) then
   finishes the tombstone as it finishes a redaction: on each target it
   writes the ledger object `tombstones/<tombstone ID>.json`, then deletes
   the key blobs and every blob under the prefixes. So a purge also reaches
   backups, and `backup check` and a restore treat the blobs of a purged
   unit as expected gaps (decisions 21 and 22). The tombstone outlives the
   unit and can outlive its namespace. So only its append carries the
   fence, and the shred operation finishes its targets without a fence, as
   the blob purge of ADR 47 does.
9. **Start-up check.** The wrapper IDs of the item key records count in
   the start-up check of ADR 23 decision 3, as the wrapper IDs of data keys
   do.
10. **No migration.** No release exists, so no installation holds content
    that the server must seal again. The change replaces the sealing of
    ADR 41 decision 10 for new content, and it includes no migration code.

### What a redaction can target

11. **What a redaction is.** A redaction destroys the content of one old
    history item and keeps its metadata. It cannot be undone. It is not
    masking: a masked state read hides secret values and keeps them
    (ADR 41). A redaction shreds the item key of the item and deletes the
    content blobs. The SQL rows of the item that hold sealed content, for
    example the event rows of an update, stay with their ciphertext, which
    nobody can open after the shred.
12. **The latest state item.** The latest state item of a unit is the set
    that ADR 150 decision 2 names, together with the replay base of each
    update that has not ended or whose journal the server did not replay
    yet. The replay base is the checkpoint that a journaled update builds on
    (ADR 43). Without this rule, a redaction of the base would make the
    checkpoint of that update impossible to build. A request for any item
    of the latest state item answers 409 with an error that says that the
    latest state cannot be redacted (`history.ErrLatest`), for every cause.
    No retention applies to state items (see "Relation to the RFCs").
13. **Active updates.** While an update on a stack is active, a redaction
    of any state item of that stack answers 409 (`history.ErrInProgress`).
    An active update is an update that runs, or a journaled update that the
    server did not replay yet. The 409 message tells the client to try again
    after the update ends, and names the one command that ends an update
    that runs: `pulumi cancel` (ADR 43 decision 10.3).

    **A replay that fails for good.** The server replays a journaled update
    when it builds its checkpoint, on `complete` and on `pulumi cancel`
    (ADR 43 decision 7.1). A build that fails for a bound of time answers
    503, and the update stays active, so the client tries `pulumi cancel`
    again. A build that fails for good is a build that cannot succeed on a
    second try: an entry that the replay cannot apply, or a size bound of
    the build (`docs/reference/journaling.md`). On `pulumi cancel`, such a
    failure does not keep the update active. The cancel ends the update as
    `cancelled` with no checkpoint of its own, and records the replay
    failure on the update, with a metric. The update is then not active,
    and the server never replays it again. Its replay base stays the latest
    state item (decision 12), so no redaction can target it. Redaction of
    the other items of the stack is then possible. The operator path to
    unblock redaction is so: `pulumi cancel`, then `pulumi refresh` or
    `pulumi up`, which makes a new update with its own checkpoint. The
    journaling guide gives these steps.
14. **The other targets.**
    - A Pulumi update that runs and an OpenTofu state version that is still
      pending answer 409.
    - A stored plan, and a closed version of a job log, can be redacted.
      A log version that is still open answers 409 (ADR 102).
    - **The current version of a job log.** The current version is the
      latest closed version of a log. A request (cause `request`) for it
      answers 409 with `history.ErrLatest`, so a person never destroys the
      only readable copy of a log. A substitution (ADR 152 decision 9) first
      writes a new version, which becomes the current version. The end of
      its retention period (cause `retention`, ADR 152 decision 5) redacts
      the current version like any other version: retention may expire it,
      a request may not. The conformance suite tests both causes
      (`historytest.SituationCurrentLog`).

    A request for an item that is already redacted answers 409 and names the
    earlier redaction.

### The redaction

15. **The checks and the write, in one fenced transaction.** The server:
    1. checks the target (decisions 12 to 14),
    2. checks that blame (ADR 150) compared the item and the next state item.
       Otherwise it answers 409, and the client can try again. Without this
       check, blame would lose the changes of the item, because blame reads
       both items,
    3. marks the item redacted in the table of its tool or of the job model,
       with the redaction ID,
    4. marks the item key record shredded,
    5. writes the redaction record: the ID (prefix `rdx-`), the item, the
       actor (ADR 150 decision 3), the time, the cause, the reason, the item
       key ID, the content blob keys, and one shred row for the live blob
       store and for each backup target that the `backups` table names,
    6. writes the audit entry (decision 26), and keeps its hash in the
       redaction record.

    A redaction is a write in a namespace, so it carries the namespace fence.
    The same transaction holds every step: a failed audit write rolls back
    the mark, the shred and the record.
16. **The shred operation.** After the commit, a durable operation (ADR 72)
    finishes the redaction:
    1. It waits until no backup that started before the commit still runs.
       Only one backup runs at a time (ADR 30). Such a backup read its SQL
       snapshot before the commit, so it names the blobs of the item, and it
       copies them from the live blob store. The wait comes before the live
       delete, so that backup never finds a blob missing and never fails.
       The wait does not leave the content readable: the key record is
       shredded at the commit, and every open checks the record first
       (decision 4). A backup that started after the commit skips the blobs
       of the item (decision 18).
    2. It deletes the key blob and the content blobs from the live blob
       store.
    3. For each backup target of the redaction, it writes the ledger object
       `redactions/<redaction ID>.json`, then deletes
       `blobs/item-keys/<item key ID>`, then deletes the content blobs
       (`blobs/<blob key>`).
    4. It marks each target done. A target that does not answer stays
       pending. The operation tries again with a back-off and counts the
       failure in a metric.

    The operation finishes a tombstone (decision 8) in the same steps: the
    ledger object is `tombstones/<tombstone ID>.json`, and the deletes are
    the key blobs of the tombstone and every blob under its prefixes.

    The redaction record answers the state of each target, so an operator
    can see which backups the redaction reached.
17. **The ledger object.** The ledger object holds the redaction ID, the
    cause, the namespace, the tool, the unit ID, the item kind and ID, the
    item key ID, the blob keys, the time and the hash of the audit entry of
    the redaction. It holds no reason and no name. The audit hash lets a
    restore link the redaction to the audit entry, when the restored audit
    log holds it.

### Backups and restore

18. **A backup skips shredded content.** A backup reads the item key
    records in the read transaction of its SQL snapshot (ADR 30
    decision 2). It does not copy the key blob or the content blobs of an
    item whose key record is shredded in that snapshot. The redaction
    record names the content blobs. So a backup that starts after a
    redaction never puts the content into a target, also a target that the
    redaction did not name.
19. **Each target gets the ledger of every redaction and every purge.** A
    backup writes the ledger object of each redaction and of each tombstone
    in its snapshot that the target does not hold yet, before it copies a
    blob. So a new target gets ledger objects for all redactions and purges
    that exist, and `backup check` and a restore of that target treat each
    gap as a redaction or a purge. The snapshot of a backup that starts
    after a purge holds no row of the purged unit, so it names no blob of
    the unit.
20. **Redaction records and tombstones survive the restore swap.** The
    tables of redaction records, of tombstones, and of their shred targets
    are control tables (ADR 72 decision 5). A restore does not replace them.
    The item key records and the tool tables are data, which the restore
    replaces.
21. **A restore applies every redaction and every purge.** A restore
    (ADR 38) applies the redactions and the purges before the swap, in this
    order:
    1. It reads the ledger objects of the source target. For each redaction
       ledger object that names an item that the live redaction records do
       not hold, it writes a redaction record with the cause `restore`, no
       reason, and the audit hash from the ledger object. For each tombstone
       ledger object that names a unit that the live tombstones do not hold,
       it writes a tombstone with the cause `purge` and the audit hash from
       the ledger object.
    2. It applies each live tombstone to the staged data: it deletes the
       purged unit from the staged data, as the final delete does, and marks
       each item key of the unit shredded. A tombstone whose unit is not in
       the staged data stays as it is.
    3. It applies each live redaction record to the staged data: it marks
       the item redacted and the item key shredded. A record whose item is
       not in the staged data stays as it is.
    4. It writes the ledger object of each live redaction record and of each
       live tombstone that the source target does not hold yet, and deletes
       from the source target the blobs that each one names.
    5. It copies no blob that a redaction or a tombstone names into the
       live blob store (`history.Gaps`).
    6. After the swap, it sets to pending again only the shred targets of
       the live blob store and of the source target, for every redaction
       and every tombstone (`RequeueShreds`, `RequeueTombstones`), and
       starts the shred operation. These are the only two places that a
       restore can bring a blob back to. A done target of another backup
       target stays done, so a restore does not make the operation write to
       every target again.

    A restore from a backup made before a redaction or a purge so stays
    redacted or purged.
22. **`backup check`, the scrub, and the orphan cleanup.**
    - `backup check` reads the ledger objects of the target, of redactions
      and of tombstones. A missing object that a ledger object names is not
      an error: a blob that a redaction names, a key blob that a tombstone
      names, and a blob under a prefix of a tombstone count as expected
      gaps (`history.Gaps`). The check reports the count of redacted items and of
      purged units. It reports a key blob that a ledger object names and
      that the target still holds, and a key record of the backup that is not
      shredded and whose key blob the target does not hold.
    - A periodic scrub compares the item key records with the key blobs in
      the live blob store and in each target. It reports a record that is not
      shredded and has no key blob: the content of that item cannot be opened.
      It deletes a key blob whose record is shredded, and counts each such
      delete in a metric.
    - The orphan cleanup (issue 203) deletes a key blob with no record only
      after a grace period of 24 hours from the write of the blob. So the
      cleanup never deletes the key blob of an item whose transaction has not
      committed yet.
    - A key blob that is missing with no ledger object and no shredded
      record stays an error in a restore, as ADR 30 decision 8 says for a
      missing blob. A restore and `backup check` share one rule for the gaps
      (`history.Gaps`), so they never disagree on a blob.

### The audit entry and the reason

23. **What a redaction keeps.**
    - Pulumi: the update row with its version, kind, result, times, actor,
      `restoredFrom`, checkpoint size and SHA-256, and its resource count.
      The event rows stay with their sequence and time, and their payload
      stays sealed.
    - OpenTofu: the state version row with its ID, lineage, serial, MD5,
      size, times, holder, actor, force flags and `restored-from`, and the
      sensitive marks (paths only, ADR 41 decision 9).
    - A plan or a log version: its row with its size, SHA-256, times and
      actor (ADR 102).
    - Every resource change row of the item (ADR 150).
    - The redaction record and the audit entry.
24. **The hashes stay.** The hashes of a redacted item stay readable, as
    documented. A hash of a whole state document does not show a value, but
    a person who knows every other byte of the document could test guesses
    for one value against it. The documents say this. A keyed hash would stop
    such guesses, but the TFE-compatible API must answer the plain MD5.
25. **No extra confirmation.** A redaction needs the reason and no other
    field, also within the recovery window of ADR 153. The audit entry
    records it.
26. **The audit entry.** Each redaction writes one audit entry of the kind
    `change`, with the action `unit:redact`, the unit as the object, the item
    ID as the version, and the reason. The same shape covers every item
    kind: state items, plans and log versions. The audit entry gets a new
    field `reason`, in the fixed hash encoding of ADR 72 decision 6. The
    server writes the entry in the transaction of decision 15, so a failed
    audit write makes the redaction fail (ADR 60). The end of a retention
    period (ADR 152) writes the same entry, with the principal `@server`,
    the cause `retention` and the reason `retention period ended`.
27. **The reason.** The reason is required for the cause `request`. It is 1
    to 1000 characters of plain text, with no control characters. Every
    reader of the audit log of the namespace can read it
    (`namespace:read-audit`), and the redaction record answers it to
    `unit:read`. So the console tells the person to describe the cause and
    not to paste the secret.

### Routes and actions

28. **Routes.** Each tool adapter serves the redaction of its own state
    items (ADR 28 decision 1). The paths are proposals, and the e2e step of
    each route sets the final path:
    - Pulumi: `POST /api/stacks/{namespace}/{project}/{stack}/updates/{version}/redact`
      with `{"reason": "..."}`. The history response gets the field
      `redaction` with `id`, `at`, `by` and `reason`. A read of a redacted
      checkpoint (`export/{version}`, the masked state read, reveal) or of
      the events of a redacted update answers 410 as
      `{"code": 410, "message": "..."}`. The message names the redaction ID.
    - TFE-compatible API: `POST /api/v2/state-versions/{state_version_id}/actions/redact`
      with a JSON:API document of the type `redactions` and the attribute
      `reason`. The state version gets the attributes `redacted-at`,
      `redacted-by` and `redaction-id`. Its `status` stays `finalized`, and
      its download URLs are null. A download of a redacted state version
      answers 410 as a JSON:API error.
    - The TFE-compatible API does not serve
      `POST /api/v2/state-versions/{id}/actions/permanently_delete_backing_data`.
      It answers 404, as for every route that it does not serve. That route
      has no request body, so it cannot carry the reason that the owner
      requires.
    - Plans and job logs: the job model serves their redaction routes
      (ADR 102). The route calls the same `Redactor` port.
    - Every tool: `GET` of one redaction record by its ID, with the state of
      each backup target, in the inventory API:
      `GET /api/inventory/redactions/{redaction_id}`.
29. **Actions.**

    | Operation | Action | Object |
    | --- | --- | --- |
    | Redact a state item, a plan or a log version | `unit:redact` | unit |
    | Read a redaction record | `unit:read` | unit |
    | Read the history with redactions | `unit:read` | unit |

    `unit:redact` is a new action, and the only action of a redaction for
    every item kind. The built-in roles of a namespace administrator and of
    a unit administrator get it (ADR 52). No other built-in role gets it.
    Each redaction also passes the policy check (ADR 17).

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **ADR 41, decision 10:** "The server seals OpenTofu state blobs and the
  `outputs` column with the data key of the workspace scope." The server now
  seals them with the item key of each state version (decisions 1 and 7).
  "The data key of a workspace goes with the final delete" stays true, and
  the purge also shreds every item key (decision 8).
- **ADR 37, decision 1:** the blob store keeps the content of each checkpoint
  as the CLI sent it. The server now seals each checkpoint blob with the
  item key of its update (decision 6). The blob keys and the SQL metadata
  of ADR 37 stay.
- **ADR 20** (the update cycle of RFC 3): the server keeps the body of each
  event as the CLI sent it. The server now seals each event payload with the
  item key of its update (decision 6). The rows, their order and the
  lease-digest check stay.
- **ADR 43, decision 6.1:** each journal batch is a write-once blob "as
  ADR 37 does for checkpoints", as the CLI sent it. The server now seals each
  batch blob with the item key of its update (decision 6). The key shape,
  the row, the order of writes and the batch SHA-256 of the plain bytes stay.
- **ADR 28, decision 3:** a restore "copies the content to a new blob key
  and checks the MD5 while it copies". The restore now opens the old content
  and seals the copy with a new item key. It still checks the MD5 of the
  plain bytes. A redacted state version cannot be restored.
- **ADR 30, decision 8, and ADR 38:** a blob that a backup names and the
  target does not hold stops a restore. A blob that a redaction names is
  now an expected gap, and so is a blob of a purged unit that a tombstone
  names. A backup skips shredded content, and a restore applies every
  redaction and every purge (decisions 18 to 22).
- **ADR 47:** the final delete deletes the rows and the blobs of a unit, and
  a blob purge record finishes the live delete. The purge now also writes a
  tombstone in a control table, and the shred operation deletes the key
  blobs and the content in every backup target (decision 8).
- **ADR 43, decision 10.3:** `pulumi cancel` builds the checkpoint first.
  When that build fails for good, the cancel now still ends the update as
  `cancelled`, with no checkpoint of its own and with the replay failure
  recorded (decision 13).

## Relation to the RFCs

- **RFC 33** proposes customer-managed keys and per-namespace keys. An item
  key is wrapped by the `Wrapper` port, as a data key is. So a namespace that
  later gets its own wrapper also wraps its item keys with it, and a
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
  when the target answers, and a backup that starts later never copies the
  content. It does not reach:
  - a copy of a target that the operator made outside the server,
  - an older object version in a bucket with object versioning, or an object
    under an object lock,
  - a target that the operator stopped using and that the `backups` table no
    longer names.
- `backup check` and the upgrade check (ADR 33) warn when a target has
  object versioning or an object lock, because a redaction cannot fully
  reach such a target. The backup guide tells the operator to turn off
  versioning for the prefix `blobs/item-keys/`.
- The disaster recovery guide says that a restore applies every redaction
  that the live installation holds, and every redaction in the ledger of the
  source target. A restore can never bring back redacted content. An
  operator who restores into a new installation keeps the ledger objects of
  the target, because the new installation has no redaction records yet.
- The live blob store holds the key blobs. An operator who copies or
  replicates the blob store outside the server also copies item keys.
  The documents say this.
- Each open of history content reads one more SQL row and one more blob (the
  key) than today. Each event read decrypts each payload. The server may
  keep wrapped keys in memory. The release load run (ADR 40) measures the
  cost.
- A rotation of the key-encryption key (the rewrap command of ADR 23) must
  write a new key blob for each item key and delete the old one, in the
  live store and in each target. A target that holds a key wrapped by the old
  key-encryption key stays readable only with that key.
- A Pulumi checkpoint blob, an event payload and a journal batch are now
  sealed at rest. This closes the gap that ADR 41 left for plain values in
  Pulumi checkpoints.
- The `secret` package gets the port `ItemKeyIndex`, the port `ItemSealer`
  and the type `ItemRef`. All are tool-neutral: the kinds are `state`,
  `plan` and `job-log` for every tool.
- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. `ItemKeyIndex`, its conformance suite (`secrettest.RunItemKeys`) and
     the SQL adapters.
  2. The item sealer, with the key blob and the open check.
  3. Seal Pulumi checkpoints, event payloads and journal batches, and move
     OpenTofu sealing to item keys.
  4. The redaction log (`history.RedactionLog`) and the tombstone log
     (`history.TombstoneLog`), their conformance suites
     (`historytest.RunRedactions`, `historytest.RunTombstones`) and the SQL
     adapters, with the redaction and tombstone tables in the control list.
  5. The `Redactor` of each tool, with `historytest.RunRedactor` and the
     table of that tool, then the routes and the audit field `reason`.
  6. The shred operation, the ledger objects, the backup skip, `backup
     check`, the scrub, the orphan cleanup grace period and the restore rule.
  7. Item key shredding and the tombstone in the purge, and the restore
     step that deletes a purged unit from the staged data.
  8. The cancel rule for a replay that fails for good (decision 13), with
     its e2e step.
  9. E2e steps: two `up` runs, a redaction of version 1, `stack history`,
     `stack export --version 1` (410), `stack export`, and `up`. A redaction
     during a running `up` (409). Two `apply` runs, a redaction of the first
     state version, `tofu state pull`, and an `apply`. A backup before the
     redaction and a restore after it, with the item still redacted. A
     backup before a `stack rm`, then a restore, with the stack still
     purged.

## Answered questions

The owner approved these answers on 2026-10-17. The decisions above apply
them.

1. **Pulumi update events.** The server seals the event payloads and the
   journal batches of each update under its item key (decisions 1 and 6).
2. **TFE backing data actions.** No alias for
   `permanently_delete_backing_data`. The route answers 404 (decision 28).
3. **Redaction within the recovery window.** No extra confirmation field
   (decision 25).
4. **Object versioning on the target.** `backup check` and the upgrade
   check warn (see "Consequences").
5. **The reason in the ledger object.** The ledger object holds the hash
   of the audit entry, not the reason (decision 17).
6. **Hashes of redacted content.** The hashes stay, as documented
   (decision 24).
