# 37. Pulumi checkpoints in blobs, and delta checkpoint uploads

Status: accepted · 2026-10-11 · decision 1 superseded in part by ADR 151

Changed by:

- ADR 41 changes decision 8: the server may read state.
- ADR 43 supersedes decision 9: no journaling.
- ADR 43 changes decisions 4 and 8: exact bytes and no state reads for a
  journaled update.
- ADR 43 extends decisions 2, 3 and 11: the key shape of journal blobs.
- ADR 45 changes decision 1: the blob holds the request envelope.
- ADR 151 changes decision 1: each checkpoint blob is sealed with the item
  key.
- ADR 220 changes decision 2: the checkpoint blob key gets a namespace prefix.

## Context

ADR 24 keeps each Pulumi checkpoint whole in a SQL column (`BLOB` in SQLite,
`BYTEA` in PostgreSQL). The server keeps one checkpoint for each mutating
update, for ever. The size limit is `DefaultMaxCheckpointBytes` in
`pkg/stack/updates.go`.

This causes problems:

- The database grows by one full checkpoint for each update. Consecutive
  checkpoints are almost the same text. Database backups, replicas and
  restores carry all of it.
- The server holds each checkpoint body in memory more than one time (issue
  208).
- A large value in SQL blocks stores and platforms with small limits: a
  DynamoDB item has a limit of 400 KB, and a Lambda request body has a limit
  of about 6 MB.
- OpenTofu does it differently. ADR 26 keeps the metadata of a state version
  in SQL and the content in write-once blobs.

The Pulumi CLI does not stream a request body. It makes the full JSON in
memory, compresses it with gzip and sends it
(`pkg/backend/httpstate/client/api.go` in pulumi/pulumi). The protocol has a
capability that lets the CLI send less:

- The server advertises `delta-checkpoint-uploads-v2` in `/api/capabilities`,
  with a `checkpointCutoffSizeBytes` value.
- The first save of an update is always a full body:
  `PATCH .../checkpointverbatim` with `version`, `untypedDeployment` and
  `sequenceNumber`. "Verbatim" means that the CLI keeps the exact JSON text.
- When the last saved deployment and the new one are both at or above the
  cutoff, the CLI sends `PATCH .../checkpointdelta`. The body has `version`,
  `checkpointHash` (SHA-256 of the result), `sequenceNumber` and
  `deploymentDelta`. The delta is a list of text edits on the last saved JSON.
- When a delta save fails, the CLI sends a full verbatim save.

The CLI also supports journaling: it sends one small entry for each resource
operation, and the server makes the checkpoint from the entries. The server
must then understand the Pulumi deployment model. This ADR does not adopt
journaling.

The project owner accepted this decision on 2026-10-11, in a conversation with
an agent session.

## Decision

1. **Content in blobs.** The blob store keeps the content of each checkpoint.
   SQL keeps the metadata: the stack ID, the update, the blob key, the size,
   the SHA-256 of the uncompressed content and the last sequence number. This
   replaces decisions 3, 6 and 7 of ADR 24. ADR 24 decisions 1, 2, 4 and 5
   stay.
2. **Blob keys.** The server writes each key one time (ADR 26). The key is
   `pulumi/<stack ID>/<update ID>/<sequence number>`. Each save writes a new
   key. The key has no namespace segment, for the same reason as in ADR 26.
3. **Order of writes.** The server writes the blob first. Then it writes the
   SQL row in the fenced transaction, with the inventory change. A row never
   names a missing blob. After the commit, the server deletes the blob of the
   previous save of the same update. A crash can leave an orphan blob. The
   sweep for orphan blobs (issue 203) covers checkpoint keys too.
4. **Exact bytes.** The server keeps the deployment JSON exactly as the CLI
   sent it. It does not decode and encode it again. The hash of a delta and a
   later delta depend on the exact text.
5. **Compression.** The server compresses each checkpoint blob with gzip. The
   SHA-256 and the size limit apply to the uncompressed content.
6. **Delta uploads.** The server advertises `delta-checkpoint-uploads-v2`. The
   cutoff has a default of 1 MiB and a flag in `cmd/opendustpan`. The server
   serves `checkpoint`, `checkpointverbatim` and `checkpointdelta`.
7. **Applying a delta.** The server reads the current checkpoint of the
   update and applies the edits in order. It compares the SHA-256 of the
   result with `checkpointHash`. A delta is valid only when:
   - the update has a checkpoint,
   - the sequence number is the next number (a repeated number with the same
     hash is a retry and succeeds),
   - every edit is inside the text, and the edits do not overlap,
   - the result is not larger than the size limit,
   - the hash agrees.

   For any other delta, the server answers 409 and saves nothing. The CLI then
   sends a full verbatim save.
8. **The server does not read state.** The server changes bytes only. It does
   not parse the resources in a checkpoint. The glossary rule ("OpenDustpan
   stores state but never reads its contents") stays true.
9. **No journaling.** `StartUpdate` answers `journalVersion` 0, so the CLI
   saves checkpoints. Journaling needs a decision on RFC 39 first.
10. **Existing rows.** Rows from before this ADR keep their content in the SQL
    column. Reads accept a row with content or a row with a blob key. A later
    command can move old rows to blobs. New saves always write blobs.
11. **Backups.** The backup of ADR 30 copies every blob that SQL names. This
    now includes checkpoint blobs.

## Consequences

- The database keeps metadata only, so it stays small. The blob store grows
  instead, until retention (RFC 18).
- During an update, a large stack sends only the changed lines. Memory use
  and network use go down for each save after the first.
- The first save of each update and `stack import` still send the full body.
  Platforms with small request limits, for example Lambda, still need a
  different path for those bodies.
- Export reads a blob and decompresses it. The wire shape of export and import
  does not change.
- Every blob adapter carries Pulumi state. The `blobtest` suite already
  applies to all adapters.
- The e2e suite must run each supported Pulumi CLI version two times: with the
  delta capability (a cutoff of 1 byte, so every save after the first is a
  delta) and without it. Both runs must give the same `stack export`.
- Delta uploads are a protocol that Pulumi does not publish. A change in the
  CLI can break them. The CLI falls back to full saves on an error, so the
  effect is lost bandwidth, not lost state.
