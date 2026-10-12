# 43. Pulumi journaling support

Status: accepted · 2026-10-12

## Context

With journaling, the Pulumi CLI sends one small journal entry for each
resource operation, in batches. The server then builds the checkpoint from
the entries. ADR 37, decision 9, keeps journaling off: `StartUpdate`
answers `journalVersion` 0, so the CLI saves checkpoints.

[RFC 62](../rfc/0062-pulumi-journaling.md) (#350) measured the benefit. At
3,000 resources, journaling took 44 s for a create and 61 s for a change.
Delta uploads (ADR 37) took 440 s and 792 s. RFC 62 also found that a
journaled update breaks ADR 37, decisions 4 and 8: the server builds the
deployment bytes, so it does not keep the bytes that the CLI sent, and it
parses resources.

[RFC 64](../rfc/0064-pulumi-journaling-architecture.md) (#394) gives the
architecture. It proposes option 1B (a package in the main module) and
option 2C (entry types generated from the upstream source), with a version
gate, strict reject, stored entries, a streaming build of the checkpoint and
three CI layers. The research note `docs/research/pulumi-journaling.md`
gives the protocol, the measured error behavior, the requirements R1 to R37
and the open questions Q1 to Q20. This ADR cites the requirements by their
numbers.

The facts from RFC 64 that shape the decisions:

- **No fallback.** After a journal error, the CLI sends no checkpoint in the
  same update. It does not retry a 400 or a 409. So a server error during a
  journaled update fails the `pulumi up` of the user.
- **The format moves under one version.** Kinds 8 and 9 and the flag
  `requiresByteString` arrived under journal version 1. A reader that
  ignores them builds a different state.
- **The CLI decides at the start.** The CLI reads `journalVersion` from the
  answer to start-update and keeps that mode for the whole update. It
  journals only for an answer of 1 or 2. Any other answer keeps checkpoints.
- **The CLI sends its version.** The `User-Agent` header has the form
  `pulumi-cli/1 (<version>; <GOOS>...)` in each CLI version that the e2e
  suite runs.

The project lead answered the open questions of RFC 64 on 2026-10-12. On
the same day, the project owner gave the decisions on RFC 62 and RFC 64 to
the project lead, and the project lead accepted this ADR. The acceptance
commit also sets RFC 62 and RFC 64 to accepted.

## Decision

This ADR accepts the proposal of RFC 64: option 1B with option 2C. The
decisions below give the exact rules.

### 1. The setting

1.1. **Journaling is on by default.** One setting in
`internal/cli/settings.go` turns it off:
`--disable-pulumi-journaling` (`DUSTPAN_DISABLE_PULUMI_JOURNALING`). It is
a `boolSetting` with the default `false`. The name follows the CLI's own
variable `PULUMI_DISABLE_JOURNALING`. There is no second setting for
journaling.

1.2. **Off answers version 0.** When the setting is `true`, start-update
and `begin-update` answer `journalVersion` 0. The CLI then saves
checkpoints, with delta uploads when the server advertises them (R2, R3).
The journal route stays in the binary.

1.3. **An update keeps its negotiated version.** The server stores the
negotiated journal version on the update row, in the fenced transaction
that starts the update. The server accepts journal batches for an update
whose row has a journal version above 0, whatever the setting says now.
The setting changes only the answer to a new start-update. So an update
that runs during a restart with a changed setting keeps its mode.

1.4. **Every binary can replay.** Each binary holds the replay code, with
the setting on or off. A journaled update that stopped before its end
still gets its checkpoint on the next read (R33, R34).

1.5. **Previews answer 0.** The server answers `journalVersion` 0 to a
preview and to a dry run. The CLI never journals them (R3).

### 2. The journal version

2.1. **The answer.** The server answers the highest journal version that
the CLI offered and that the server supports, only to a CLI that the
version gate lets through (decision 5). It never answers a version that
the CLI did not offer (R2).

2.2. **Version 2 before the release.** The server must support journal
version 2 with state-migration replay (R29) before journaling is part of a
release. Version 1 alone is not enough for a release. The reason is a
failure that a checkpoint user does not see:

- A CLI of v3.264.0 or later offers 2. When the server answers 1, the
  resource monitor of the CLI does not advertise the state-migration
  feature. It refuses a resource registration that carries a state
  migration with "state migrations are not supported by this deployment".
  That fails the update.
- With checkpoints, or with version 2, the same CLI allows the migration.

This comes from the CLI source (research note, section 4, v3.268.0, and
RFC 64, open question 3). No run measured it.

2.3. **A measurement before the release.** An e2e step must measure the
case of decision 2.2 before the release. The step runs a program whose
SDK sends a state migration, with a CLI of v3.264.0 or later, in three
modes: checkpoints, journal version 1 and journal version 2. It records
the CLI result and the `stack export` of each mode. Version 2 must give
the same normalized export as checkpoints (decision 11.3). If the step
shows a result that is different from decision 2.2, the project owner
decides again.

2.4. **The release gate.** No release answers a non-zero journal version
before the work meets decisions 2.2, 2.3, 9 and 10.4. The work items at the end
of this ADR give the order.

### 3. The module boundary

3.1. **A package in the main module.** The journal code lives in
`internal/pulumijournal`, in the main module
`opendustpan.org/opendustpan`. There is no separate Go module and no own
version tag.

3.2. **ADR 7 applies as it does today.** This ADR adds no Go dependency
and no module of this project. So the two-week rule of ADR 7 applies to
the dependencies of the main module only, as it does today.

3.3. **Imports.** A depguard rule forbids `internal/pulumijournal` to
import `pkg/...`, `internal/api/...`, `net/http`, `database/sql`, any
store or blob package, and the fence. The package takes bytes and readers,
and gives bytes, writers and errors. So a later move to a separate module
is a move of files.

3.4. **A port in `pkg/stack`.** `pkg/stack` declares a small interface for
replay and calls the package through it (CLAUDE.md, "Define interfaces in
the package that consumes them"). The interface gets a conformance suite.
The corpus of decision 11.3 is its input.

3.5. **What reopens decision 3.1.** One of these events reopens the choice
of a separate module (RFC 64, section 1). Each needs a new RFC:

- the first SDK embedder (ADR 35) that needs journaling and cannot wait for
  a server release to get a journal fix,
- a need to ship journal fixes faster than server releases, for example a
  server release cadence that is slower than the Pulumi release cadence
  (about one release each 5.4 days, RFC 64, section 3),
- a public API in `pkg/` that must expose journal types, for example a
  replay call for embedders.

### 4. The replayer

4.1. **A port of the upstream replayer.** The replay code is a port of
`pkg/backend/journal.go` and `pkg/backend/journal_state_migration.go` from
pulumi/pulumi at the pinned commit of decision 8.1. It does not import
`github.com/pulumi/pulumi/pkg/v3` or `github.com/pulumi/pulumi/sdk/v3`.

4.2. **Attribution.** pulumi/pulumi is Apache-2.0 and has no `NOTICE` file
at `v3.268.0`. Each ported file keeps the upstream copyright line, says
that this project changed it, and names the upstream commit (Apache-2.0,
sections 4(b) and 4(c)). ADR 22 makes the licenses compatible.
`goheader` stays off in `.golangci.yml`. A test in the package fails when
a ported file does not have the header.

4.3. **Same rules as upstream.** Replay follows the upstream replayer at
the pin: R18 to R28 for version 1, and R29 for version 2. This includes
`NormalizeURNReferences` (R27), which the spike port does not do. URN
normalization reads link fields only, so the port does it on the link
fields.

4.4. **No secrets.** Replay copies ciphertext and decrypts nothing (R31).
Replay needs no new action and writes no audit entry. A masked read
(ADR 41, decision 2) of a journaled update reads the built checkpoint like
any other checkpoint.

### 5. The version gate and strict reject

5.1. **The CLI version.** The server reads the CLI version from the
`User-Agent` header, in the form `pulumi-cli/1 (<version>; ...)`.

5.2. **The newest verified CLI version is a constant.** The binary holds
the newest verified CLI version as a constant. No setting changes it. The
cost: a CLI newer than that constant saves checkpoints until the operator
upgrades the server to a binary that verified it. The update works, but
without the speed of journaling.

5.3. **The oldest verified CLI version is a constant too.** The binary
holds the oldest CLI version that the corpus (decision 11.3) holds as a
second constant. The first value is v3.250.0, the oldest journaling version
in the e2e matrix today.

5.4. **The gate.** The server answers a non-zero journal version only to a
CLI whose version is a release version from the oldest to the newest
verified CLI version. It answers 0 to each other CLI: an empty version, a
version that the server cannot parse, a development build (`-dev`), and a
version outside the range. A gated CLI saves checkpoints (R2, R3).

5.5. **Strict reject.** The server refuses a batch with an entry that it
does not know: an unknown kind, an unknown field, a version of 0 or less,
a version above the negotiated one, kind 10 with a version other than 2,
or a `sequenceID` of 0 (R14, R17). It answers 400 with a message that names the entry. It
stores nothing from that batch, and it records the refusal (decision
10.1). The server never ignores what it does not know.

5.6. **The gate is not a security control.** A proxy can change
`User-Agent`, and any client can send any value. A client that claims an
old version and sends a new kind meets strict reject and fails its update.

5.7. **What the operator does when strict reject fails an update.** The
operator reference must give these steps:

1. Set `DUSTPAN_DISABLE_PULUMI_JOURNALING=true` (or the flag) and restart
   the server. Each new update then gets `journalVersion` 0 and saves
   checkpoints, with the same CLI.
2. The update that failed is already at its end: the CLI sent `complete`
   with `failed`. The user runs `pulumi up` again. That update saves
   checkpoints. A `pulumi refresh` can be necessary when the failed update
   left resources that exist but are not in the state.
3. An update that runs in journal mode during the restart keeps that mode
   (decision 1.3). The CLI tries a transport error again up to 4 attempts,
   1 s, 2 s and 4 s apart (research note, section 5.6, from the source, not
   measured). A restart that takes more than about 7 s can fail that update
   too. The user then runs `pulumi up` again.
4. The operator reports the program and the CLI version in an issue in
   this repository, so that a person can add the case to the corpus and fix
   the replay.

### 6. Storage of entries

6.1. **Entries are write-once blobs.** The server writes each accepted
batch as one write-once blob, as ADR 37 does for checkpoints. The key is
`pulumi/<stack ID>/<update ID>/journal/<lowest sequence ID of the batch>`.
The server writes the blob first. Then it writes the SQL row in the fenced
transaction that also verifies the lease digest (ADR 20). The row names the
blob and holds the sequence IDs and the operation IDs of the batch, the
SHA-256 of the batch and an arrival number (decision 6.6). A row never
names a missing blob. The sweep for orphan blobs (issue 203) covers journal
keys.

6.2. **2xx means stored.** The server answers 2xx only after the commit of
the row (R11).

6.3. **A retry stores nothing.** When each sequence ID of a batch is
already stored with the same batch SHA-256, the server answers 2xx and
writes nothing (R12). When a batch holds a sequence ID that is already
stored with different content, the server answers 409 and records the
refusal (decision 10.1).

6.4. **Verify on arrival.** The server verifies each entry when its batch
arrives: the rules of decision 5.5, and each reference to an earlier entry
(`removeNew`, `deleteNew`, `pendingReplacementNew`, new-state patches)
names an entry that the server holds. So each stored entry can replay, and
the stored set never refers to a missing entry.

6.5. **Batches after a refused batch.** The CLI continues to send batches
after a refusal (R35). The server stores each later batch that passes
decision 6.4, because it records changes that the CLI made to real
resources (R34). A later entry that refers to an entry of a refused batch
fails decision 6.4 too, and the server refuses its batch. So a refusal never
leaves a stored entry that refers to a missing entry.

6.6. **Order.** Replay applies the stored entries in the order of
arrival, as the replayer of the CLI does. That replayer is the reference:
it applies entries in the order that it gets them and does not sort. The
order of the entries in a batch can be different from the `sequenceID`
order (research note, section 5.5). The key of decision 6.1 holds the
lowest sequence ID, so it does not give the order of arrival. So the row
of each batch holds an arrival number: the server gives the next number of
the update in the fenced transaction that stores the batch. Replay reads
the batches by arrival number, and the entries of a batch in the order of
the body. The arrival order meets R16 for each CLI that the gate lets
through (v3.210.0 and later send one batch at a time). Sequence IDs serve
only to find a retry (decision 6.3) and a gap (decision 10.2).

6.7. **The server deletes the entries after the build.** The built
checkpoint of decision 7.1 is the record of the update. The server deletes
the journal rows of the update in the fenced transaction that writes the
checkpoint row. After the commit, it deletes the journal blobs, as ADR 37,
decision 3, does for the blob of a previous save. The sweep for orphan
blobs deletes what a crash leaves. The corpus of decision 11.3, not
production data, serves to debug replay.

6.8. **Backups.** The backup of ADR 30 copies every blob that SQL names.
So a backup holds the journal blobs of each update that runs at the time
of the backup, like any live data. A backup never holds the journal blobs
of an update that ended, because SQL no longer names them. A restore of
such a backup gives the running update its stored entries back, and the
update ends by `complete` or `pulumi cancel` as usual.

### 7. The build of the checkpoint

7.1. **At the end of the update.** On `complete` and on cancel, the server
builds the checkpoint of the update from the base checkpoint and the
stored entries. It stores the result through the checkpoint path of
ADR 37: a write-once blob with gzip, then the fenced row. After that, the
update has a checkpoint like any other update. A later update uses the
checkpoint or delta rules of ADR 37 on it.

7.2. **On read, with no cache, for an update that did not end.** When a
read needs the state of a journaled update that did not end (export, the
`deployment` of `begin-update`, a masked read), the server builds the
checkpoint from the stored entries for that read. It keeps no cache. A new
decision can add a cache only after a measurement shows the need.

7.3. **A streaming build.** The build never holds a generic tree or a full
copy of the state in memory (#389). It reads the base checkpoint with a
streaming decoder, one resource at a time. It writes each resource as raw
bytes, or with its link fields changed. The output goes through gzip and
SHA-256 into `blob.Store.Put`, or into the response of a read. In memory,
it holds the replay maps, the new resource states of the update and an
index of the link fields of each resource. A refresh and URN normalization
need the link index before the build writes the first resource, so the
build can read the base two times.

7.4. **The base.** Replay starts from the checkpoint that the server
served at the start of the update, with the same resource order (R18).

### 8. Wire types and the decoder

8.1. **Generated entry types.** A generator in
`internal/pulumijournal/internal/gen` writes the Go types for the journal
entry, the batch body and the kind constants. Its input is
`sdk/go/common/apitype/journal.go` of pulumi/pulumi at the pinned commit
`128d3f2ec94e147915a6a928fdf7449d561d560e`, the tag `v3.268.0`. A small
file next to the generator holds the commit, the tag and the SHA-256 of the
input file.

8.2. **No network.** The generator reads a copy of the input file in
`testdata/`. It stops with an error when the SHA-256 of the copy is not the
pinned hash. So `make generate` needs no network, and a hand edit of the
copy fails.

8.3. **The output.** Fields of resource states, operations, deployments,
secrets providers and snippets become raw JSON. So the output has no
generic tree and imports nothing from Pulumi. The link fields that replay
reads have their own hand-written types in the package. The generated file
has the Apache-2.0 header of the input file and a line that names the
commit and the tag.

8.4. **In `make generate` and CI.** `make generate` runs the generator
after `sqlc generate`. `make lint` runs the generator into a temporary
directory and fails on a difference with the file in the tree, as
`sqlc diff` does. So a hand edit of the generated file fails CI.

8.5. **How the pin moves.** One pull request moves the pin, in these
steps:

1. Layer B (decision 11.2) opens an issue with the upstream diff.
2. A person marks the diff client-only, additive, semantic or wire-break,
   as the research note does.
3. The person copies the input file at the new tag into `testdata/`,
   changes the commit, the tag and the hash, and runs `make generate`.
4. The person changes the replay code when the diff needs it, and changes
   the ported files and their header lines to the new commit.
5. The person records the new CLI release in the corpus (decision 11.3).
6. The corpus test passes, and the e2e run of that CLI with journaling on
   and off gives the same normalized export.
7. Only then, the same pull request moves the newest verified CLI version
   of decision 5.2 to that release.

8.6. **The decoder.** The journal code decodes and encodes JSON with
`encoding/json/v2` and `encoding/json/jsontext` from the Go standard
library. In Go 1.27.2 they are on by default, with no `GOEXPERIMENT`
(RFC 64, section 2). The code uses no third-party JSON library, and the
generator of decision 8.1 adds no dependency. Streaming reads use
`jsontext`: the build of decision 7.3, and lookups in state such as the fix
of #389. The source of this choice is the JSON libraries research note
(`docs/research/json-libraries.md`, #396). RFC 64 found no text that
puts `encoding/json/v2` under the Go 1 compatibility promise, so a later Go
release could change its API.

8.7. **A third-party decoder needs a new ADR.** A change to a third-party
JSON decoder needs a new ADR and the approval of the project owner under
the dependency rule of CLAUDE.md.

### 9. Bounds

9.1. **Bounds are numbers with a measurement.** Each bound below must have
a number in the code and in the operator reference before journaling is
part of a release. A measurement must support each number. This ADR gives
the requirement and the measurement, not the numbers. A comment next to
each number names its measurement.

9.2. **One batch.** The server answers 413 to a batch that is larger than
the batch bound, and the CLI splits it (R10). Measure: the largest batch
body in the corpus and in the sized e2e runs.

9.3. **One update.** A bound on the number of entries, the bytes of new
resource states and the size of the link index of one update. The existing
`max-checkpoint-bytes` setting stays the bound of the built checkpoint.
Measure: these three values at 3,000 and 10,000 resources, for a create
and for a change.

9.4. **Memory of the build.** A bound on the memory that one build uses.
Measure: a Go benchmark in `internal/pulumijournal` that reports the peak
heap of a build at 3,000 and 10,000 resources, for a create and for a
change (RFC 64, open question 7). The bound must not grow with the inputs
and outputs of the base resources.

9.5. **Time of the build at `complete`.** A bound on the time of the build
of decision 7.1. The CLI waits for the answer to `complete` (RFC 64, open
question 6). Measure: the build time at 3,000 and 10,000 resources, and
what the CLI does when the server holds the `complete` call longer than
20 minutes (research note, Q15). The measurement must show that the bound
stays below the time after which the CLI stops.

### 10. Refused batches and the end of an update (R37)

10.1. **Record each refusal.** The server records each refused batch of an
update: the sequence IDs that it could read, the status and the reason. It
also records a batch that failed to store.

10.2. **A gap or a refusal never ends as `succeeded`.** The server refuses
`complete` with status `succeeded` with 409 when the update has a recorded
refusal or a gap in its stored sequence IDs. A gap is a missing sequence
ID between 1 and the highest stored sequence ID. The 409 message says that
a journal batch was not stored and names the one command that helps:
`pulumi cancel`. The update stays in progress, and the stack stays locked.

10.3. **The way out is `pulumi cancel`.** An expired lease does not free
the stack (ADR 20, decision 1). Anyone with the right to update the stack
may cancel, with no lease (ADR 20, decision 2). The cancel route ends a
journaled update like any other update. It builds the checkpoint from the
stored entries first (decision 7.1), with unfinished operations as pending
operations (R33, R34). Until RFC 18 adds a force release, `pulumi cancel`
is the only way out, as for any update after ADR 20.

10.4. **The e2e step.** An e2e step must prove the way out before
journaling is part of a release. It makes the server refuse a last batch
that holds only elided entries. Then the CLI sends `complete` with
`succeeded`, and the server refuses it. Then the step runs
`pulumi cancel`, and then a `pulumi up` that succeeds. It compares the
export after the cancel with the stored entries. This step answers Q19 of
the research note for this server.

10.5. **Other ends.** The server accepts `complete` with `failed` or
`cancelled` as today. It builds the checkpoint first (decision 7.1).

10.6. **Limit.** Decisions 10.1 and 10.2 close the case of a batch that the
server saw: a refused batch, or a batch that failed to store. They cannot
close the case of a last batch that never arrived. After its retries, the
CLI ignores the error of a batch that holds only elided entries, and then
sends `complete` with `succeeded` (`pkg/backend/httpstate/journal/snapshot.go`
at 128d3f2: `ElideWrite` in `AddJournalEntry`, and `Close`). If that batch
never reached the server, the server has no refusal to record. It sees no
gap, because the missing sequence IDs come after the highest stored one.
`complete` passes, and the checkpoint lacks those entries. The protocol
carries no count or end marker that could show the loss: `complete` holds
only `status` (`CompleteUpdateRequest`), and a batch holds only `entries`
(`JournalEntries`). No signal exists to detect this case. This case is not
measured.

### 11. CI layers

11.1. **Layer A: nightly run with the newest CLI release.** A scheduled
workflow runs the Pulumi part of the e2e suite each night with the newest
CLI release, with journaling on and off. It catches a release on a day
with no push to `main`. A red run gets an issue in this repository. A run
with a CLI built from upstream `master` is not part of this ADR. It stays a
later option, with the cost that RFC 64, section 3, measured: about 5 more
minutes and about 3 GB of disk each night, about 150 runner minutes each
month, and a change in the e2e harness for a local CLI binary. A build of
`master` can also fail for reasons that are not about journaling.

11.2. **Layer B: upstream watch.** A scheduled workflow clones
pulumi/pulumi with `--filter=blob:none` and makes a diff of the strict and
the wide file sets of RFC 64, section 3, between the pin and the newest
release tag. For the wide set, it reports only commits that match
`[Jj]ournal`. When a file changed, it opens an issue in this repository
with the diff and the `needs-triage` label. It reads the public source
only. It sends nothing to the Pulumi project: no issue, pull request,
discussion, comment or post.

11.3. **Layer C: a recorded corpus with an equivalence test.** For each
verified CLI release, the e2e recorder keeps the base deployment that the
server served, each journal batch body, and the `stack export` of the same
program with journaling off. A unit test in `internal/pulumijournal`
replays the batches on the base and compares the result with the export,
after the normalization of RFC 62, question 2. The test also replays the
same batches in `sequenceID` order and compares that result too. A
difference is a test failure that a person must examine. It does not
change the order of decision 6.6. The corpus covers each kind
(0 to 9 for version 1, and 10 for version 2), an update in place, a
refresh that deletes, a crash, a failed step, pending operations,
`requiresByteString`, snippets and state migrations.

11.4. **Both modes in e2e.** The e2e suite runs each supported CLI version
with journaling on and with journaling off. Both runs must give the same
normalized `stack export`, as ADR 37 asks for delta uploads.

### 12. What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

12.1. **ADR 37, decision 9** ("No journaling"): replaced. The server
answers a journal version by decisions 1, 2 and 5.

12.2. **ADR 37, decision 4** ("Exact bytes"): does not apply to a
journaled update. The server builds the deployment bytes of a journaled
update from the entries (decision 7). Full and delta saves keep exact
bytes.

12.3. **ADR 37, decision 8** ("The server does not read state"): does not
apply to a journaled update. Replay parses the entries and the link fields
of the base resources. ADR 41, decision 1, allows the read. ADR 41 does
not name ADR 37, decision 8, so this ADR does.

12.4. **ADR 37, decisions 2, 3 and 11**: stay. Decisions 6.1, 6.7 and
6.8 add the key shape of journal blobs, with the same order of writes, the
same delete after the commit, the same sweep and the same backups.

12.5. **ADR 20, decision 5**: this ADR replaces the journaling part
("advertises ... no journaling"). ADR 37 replaced the delta part.

## Consequences

- Each update of a CLI that the gate lets through journals by default.
  RFC 62 measured 10 to 13 times less time at 3,000 resources than delta
  uploads.
- A server error during a journaled update fails the update of the user,
  because the CLI has no fallback. The gate, strict reject, the corpus and
  the nightly run make this rare, not impossible. The operator's way out is
  the setting of decision 1 and a restart.
- A new CLI release saves checkpoints until a binary verifies it. Users of
  a new CLI lose speed, not state.
- Each Pulumi release that changes the journal needs a person: layer B
  found a change in 9 of the 18 releases from v3.251.0 to v3.268.0
  (RFC 64, section 3).
- The server now parses Pulumi state for journaled updates. A replay bug
  can build a wrong checkpoint. The server deletes the entries after the
  build (decision 6.7), so a fixed binary cannot build that checkpoint
  again. The next update of the stack, or a `pulumi refresh`, must fix the
  state. The corpus holds the cases for a replay fix.
- If the last journal batch never reaches the server, the server cannot
  see the loss (decision 10.6). The update ends as `succeeded`, and the
  checkpoint lacks those entries. A `pulumi refresh` fixes the outputs.
- The blob store holds the journal blobs of an update only while it runs:
  about 450 KB for a create of 3,000 resources (RFC 62).
- A downgrade to a binary without the journal route answers 404 to a
  batch, and the CLI does not retry it. `opendustpan upgrade check`
  (ADR 33) must report an update that runs in journal mode.
- The ported files and the generated file carry Apache-2.0 headers of
  pulumi/pulumi. The rest of the repository keeps the root `LICENSE` only.

## Work items

These become issues when the project owner accepts this ADR. No issue
exists yet. Each item names the items that it needs.

1. **Pulumi journaling: settings and negotiation with answer 0.** The
   setting of decision 1, the journal version on the update row, and the
   answer 0 for previews. Needs nothing.
2. **Pulumi journaling: version gate.** Parse `User-Agent`, the two
   constants, and the gate of decision 5, with unit tests for each form of
   the header. Needs 1.
3. **Pulumi journaling: package and depguard rule.**
   `internal/pulumijournal`, the depguard rule and the replay interface in
   `pkg/stack` (decision 3). Needs nothing.
4. **Pulumi journaling: generated entry types.** The generator, the pinned
   input in `testdata/`, `make generate` and the diff in `make lint`
   (decision 8). Needs 3.
5. **Pulumi journaling: decoder.** `encoding/json/v2` and `jsontext` in
   the generated types and the streaming reads (decision 8.6). Needs 4.
6. **Pulumi journaling: version 1 replayer port.** Port with attribution,
   the header test, and URN normalization (decision 4). Needs 4 and 5.
7. **Pulumi journaling: corpus and equivalence test.** Recorder changes and
   the unit test of layer C, in arrival order and in `sequenceID` order
   (decision 11.3). Needs 6.
8. **Pulumi journaling: entry storage.** The migration, the store methods
   with conformance tests, the blobs, the arrival number, retry and
   verification on arrival, the delete after the build, the sweep and the
   backups (decision 6). Needs 3.
9. **Pulumi journaling: journal route.** `PATCH .../journalentries` with
   `unit:write`, gzip bodies, strict reject and 413, with its e2e step
   first (decisions 5.5 and 9.2). Needs 2, 6 and 8.
10. **Pulumi journaling: streaming build.** The build at `complete`, at
    cancel and on read, and the delete of the entries after the build
    (decisions 6.7 and 7). Needs 6 and 8.
11. **Pulumi journaling: refusals, `complete` and cancel.** Decision 10,
    with the e2e step of decision 10.4. Needs 9 and 10.
12. **Pulumi journaling: version 2 and state-migration replay.** Port of
    `journal_state_migration.go` (R29), corpus cases for kind 10, and the
    e2e step of decision 2.3. Needs 7 and 11.
13. **Pulumi journaling: bounds.** The measurements and the numbers of
    decision 9, with the benchmark. Needs 10.
14. **Pulumi journaling: e2e in both modes.** Each supported CLI version
    with journaling on and off (decision 11.4). Needs 11.
15. **Pulumi journaling: nightly run (layer A).** Needs 14.
16. **Pulumi journaling: upstream watch (layer B).** Needs 4.
17. **Pulumi journaling: journaled updates in the upgrade report.** A line
    in the report of `opendustpan upgrade check` (ADR 33). Needs 8.
18. **Pulumi journaling: operator reference and glossary.** The setting,
    the steps of decision 5.7, the bounds, and glossary terms for journal
    entry and journal version. Needs 12 and 13.
19. **Pulumi journaling: release gate.** A release answers a non-zero
    journal version only after the work finishes items 11 to 15 and 18
    (decision 2.4). Needs 11 to 15 and 18.
