---
rfc: 126
title: "Placement, routing, replication and moves: one design for RFCs 37, 66 and 88"
status: draft
created: 2026-11-07
---

# RFC 126. Placement, routing, replication and moves: one design for RFCs 37, 66 and 88

Status: draft. Design only. It decides nothing. It gives one design for
three open items, because they share one mechanism:

- RFC 37: where a namespace lives and how a request reaches it.
- RFC 88 and ADR 248, section "Not decided": request forwarding,
  replication, failover, the divergent tail, keys, the discovery document and
  joins (issues #1051, #1052, #1054, #1055, #1056 and #1058). RFC 79 is
  dropped. Its useful parts are already in RFC 88.
- RFC 66: a move of units and namespaces. The owner decided on 2026-11-07
  (ADR 346): no one-click move between namespaces, and a move between
  collections lists what breaks. A move between installations stays open.

RFC 88 is accepted and does not change (ADR 15). This RFC proposes the
answers that ADR 248 leaves to its design issues. RFC 37 and RFC 66 point
here.

Revision of 2026-11-07, after an adversarial review on the pull request:
the planned failover waits before the switch, never after it; the replica
cursor is `(failover version, seq)` with a failover history; the fence also
checks the primary installation; and new sections cover a peer that can widen
access, work in flight at a failover, schema skew and the revocation of a
replica's keys.

Revision 2 of 2026-11-07, after the second review: the checks of each row
that `ApplyReplicated` applies (B1a), runner jobs at a failover, the fenced
peers list, an end state of a shred for a copy that never applies, a source
that is no longer the primary, and signed proof of each failover version.

## Owner direction

- ADR 248: installations own namespaces. One primary installation for each
  namespace, asynchronous replication to replica installations, manual
  failover, optional request forwarding. No write waits for a peer. No
  quorum, no consensus, no lease, no control home, no namespace directory
  with one writer.
- ADR 280: milestone 7 is one installation on one database. Federation stays
  in "Later". So nothing below blocks the first tag, except the one-way
  doors.
- The owner on 2026-11-07, as the project manager relayed: these are solved
  problems. Find the known patterns and apply them.

## The known patterns, and what this design takes

| Pattern | What it does | What this design takes | What it does not take |
| --- | --- | --- | --- |
| AWS cell-based architecture (Well-Architected whitepaper "Reducing the scope of impact with cell-based architecture"), Azure deployment stamps, Slack cells | Many full copies of the stack (cells, stamps). A thin router maps a partition key to a cell. A cell migration moves one partition. | An installation is a cell. The namespace is the partition key. The router is thin and reads a map. A move of a namespace is a cell migration. | A router with logic of its own. Our router only forwards. |
| Tenant routing directories (Shopify "Sorting Hat", Slack, Vitess `vtgate` with a keyspace map) | A lookup table from tenant to shard. IDs carry no shard. A move changes one row of the table. | A map from namespace to primary installation, which is the namespace row itself. IDs carry no installation. | A global directory with one writer (ADR 248 forbids it). |
| CockroachDB `REGIONAL BY ROW`, Spanner leader placement, YugabyteDB tablespaces and geo-partitioning | The database places each row near its home. Writes use a consensus group of replicas. | The idea that one place owns each row. | The database itself. Each write waits for a quorum, which ADR 248 rejects, and SQLite cannot do it. |
| YugabyteDB xCluster, PostgreSQL logical replication with publication row filters (PostgreSQL 15 and later) | Asynchronous, one-way replication of rows, selected by a filter. | One-way flow, a copy then a stream, a cursor on the replica. | The database mechanism itself: no SQLite support, no blobs, no failover-version check on apply, and a filter cannot select by namespace for tables that hold the name and not the ID. |
| PostgreSQL timelines, Kafka leader epochs | Each failover starts a new timeline or epoch, and records where it forked. A follower compares its last position with the fork point, and truncates or copies again. | The cursor `(failover version, seq)` and the failover history of each namespace (B3). | Truncation in place. A replica past a fork copies again. |
| GitLab Geo | One primary site, secondary sites. Database by PostgreSQL streaming replication, files by a separate log cursor and verification. Secondaries proxy writes to the primary. Planned failover uses maintenance mode first. | Secondaries forward writes. A planned failover pauses writes first. Verification of what a replica holds. | Two replication mechanisms (database and files). Here one log carries rows and blob references. |
| Vault performance replication | Primary cluster owns writes. Performance secondaries serve reads and forward writes. Local mounts and local tokens never replicate. | A local installation scope that never replicates. Tokens of the installation scope work only on their installation (ADR 248 decision 13). | A secondary that holds its own writable data in a replicated namespace. |
| Consul WAN federation and cluster peering, Nomad multi-region | Each datacenter or region has its own consensus and no consensus across them. A request names its target and is forwarded. Peering exports and imports services explicitly, one side at a time. | Forwarding by name. No consensus across installations. A join between namespaces has two halves, one on each side (RFC 79 section 8). | ACL replication from one authoritative region (that is a control home). |
| Temporal global namespaces | One active cluster for each namespace, a failover version with the cluster index in its low digits, asynchronous replication, and a graceful failover with a `Handover` state that pauses writes until the replica catches up. | All of it. ADR 4 and ADR 248 already follow it. The planned failover below is the Temporal handover, and the failover version takes the index form (B3). | Nothing. |
| Shopify pod moves (Ghostferry), Vitess `MoveTables`, Slack workspace migration | Copy the data, then tail the change stream, then a short write freeze and a cutover of the routing row, then a cleanup. | The move of a namespace between installations: a replica, then a planned failover, then a detach. | Dual writes. Here no write ever goes to two places. |
| Fencing tokens and epochs (Martin Kleppmann, "How to do distributed locking"; Chubby sequencers) | Each writer carries an epoch, and the storage refuses a write with an old epoch. The epoch must name the writer. | The namespace fence and the failover version (ADR 4), with the primary installation checked in the same transaction (door 3). | Nothing. |

The common answer of these systems: one writer for each partition, a map
from partition to writer, a log from writer to copies, a history of epochs,
and a cutover that raises an epoch. OpenDustpan has the partition (the
namespace), the writer (the primary installation) and the epoch (the failover
version). The fence does not yet check the writer. The log, the history,
the forwarding and the cutover do not exist. This RFC designs those parts.

## What is in place

Facts from `main` at commit `fa62d37`:

- `pkg/namespace`: `Namespace.PrimaryInstallation`, `FailoverVersion`,
  `Fence{Namespace Name, FailoverVersion}`, `ErrNotHome`, `ErrFenced`,
  `WritableHere`.
- The store checks the failover version of the fence in the transaction of
  each write (`checkFenceForUpdate` in `pkg/store/postgres/audit.go`). It
  does not check the primary installation.
- 114 files outside tests call `.Fence()`, and 15 call `WritableHere`. Some
  fence sources read the namespace with `Get`, not `WritableHere`, for
  example `namespaceFences.Fence` (`pkg/dustpan/stateguard.go`, gates) and
  `jobFences.Fence` (`pkg/dustpan/jobservice.go`, jobs). With one
  installation this is correct. On a replica, such a write carries a valid
  local version and passes the store check.
- Namespace IDs are `ns-` and 20 random lowercase letters and digits
  (`internal/ident.NewID`). All other object IDs use the same function with
  their own prefix (`ws-`, `prn-`, `grt-` and others). So IDs are unique
  across installations with no coordination, and they hold no placement. The
  store never reuses a namespace ID: a later namespace of the same name gets
  a new ID.
- Namespace blobs are under `namespaces/<namespace ID>/`, and
  `pkg/blob/scoped.go` refuses a key outside that prefix.
- A restore raises the failover version of each namespace (ADR 38).
- The inventory keeps a change feed for each namespace:
  `inventory_change_heads` and `inventory_changes (namespace_id, seq)`. The
  head row is locked in the write transaction, so commit order equals `seq`
  order. It is the same shape as the replication log below, for one table.
- A manual lock can freeze a whole namespace (ADR 213).
- ADR 218 defines the backup of one namespace in the portable format of
  ADR 42. It is not built (`pkg/backup` has no namespace scope).
- The installation API answers `primaryInstallation`.
- `ErrNotHome` maps to 421 in the Pulumi, TFE-compatible and OpenTofu http
  adapters, but to 503 in the runner API. The answers do not agree.
- The move check (ADR 244) and the import (ADR 241) exist for units.
- Nothing forwards, replicates, fails over or moves a namespace.

## Part A. Placement and routing (RFC 37, issue #1054)

### A1. The map is the namespace row, and a static peer list

- **Placement of a namespace that this installation holds**: the namespace
  row, as today. A replica holds a copy of the row, so it knows the primary
  installation. No directory is needed.
- **Peers**: a static list in the static layer of RFC 68. Each entry has an
  installation name, a small installation index (1 to 99, unique in the
  list, B3), a public URL, an internal URL, the identity of its TLS client
  certificate, its public transport key (B7) and its public signing keys for
  the failover history (B3a): one current key, and the retired and revoked
  keys, each with its key ID and its time. The static layer only,
  because an entry decides where client tokens and namespace keys go
  (RFC 37 design point 1, "Security").
- **Placement entries for namespaces that this installation does not hold**:
  an optional static list of `namespace name -> installation name`. A
  front door with no copy (for example the router of OpenDustpan Cloud in
  front of several installations) uses it. OpenDustpan Cloud writes this
  file from its sign-up shim. That is the cell router of AWS and Slack, with
  the map in a file. The same Go code reads it in every build (RFC 37 owner
  constraint 1).
- **Placement from peers.** At start, and then on a timer, each installation
  reads `(failover version, primary installation)` of each namespace that it
  holds from each peer. The highest failover version wins (B3). This read
  never blocks a write. It only lets an old primary or a third installation
  learn of a failover that it did not see in its own stream.
- `GET /api/server/placements/{namespace}` answers the placement with its
  source (`namespace row`, `replica row`, `peer` or `placement entry`), so a
  person can see why a request went where it went. An edge proxy can cache
  it. Its action is `server:placement-read`.

### A2. The router

A middleware in `pkg/dustpan`, before each tool adapter. Each adapter gives a
resolver from a request to a namespace, as the route table already resolves
the object of each action. The order:

1. The namespace is local and this installation is its primary: serve.
2. The namespace is local as a replica: a read of the console inventory or
   the context API is served from the copy, with the replication lag in the
   answer, only while the lag is below a limit (60 seconds by default).
   Above the limit, the read is forwarded. Each request of a tool API is
   forwarded, because a CLI reads what it just wrote.
3. A placement entry names another installation: forward.
4. Otherwise: not found, as today.

A route with an ID and no namespace (`/api/v2/workspaces/{id}`, state
versions, uploads, runs):

- A unit never moves between namespaces (ADR 346), and the store never reuses
  a namespace ID. So the map from a unit ID to its **namespace ID** never
  changes. The cache holds the namespace ID, never the name, and never the
  installation. The router reads the installation from the placement each
  time.
- The router finds the ID in the local rows (primary or replica) first.
- On a miss, before any call to a peer:
  - the ID must parse with a known prefix;
  - a rate limit applies for each client address and for the whole
    installation, before the fan-out;
  - at most one fan-out for an ID runs at a time, and others wait for it.
- The router asks each peer with `GET /api/server/placements/by-id/{id}`,
  in parallel, with the timeout of the forward. A peer answers this route
  only to a client certificate in its peer list, and the route declares the
  action `server:placement-read` in the route test.
- One peer claims the ID: cache the namespace ID, with no end time. Two or
  more peers claim it with different namespace IDs: answer an error, cache
  nothing, and write an audit entry that names the peers. No peer claims
  it: cache the miss for 10 seconds.
- A cached entry is checked at use: when the primary answers that the ID is
  not in that namespace, the router deletes the entry.
  `DELETE /api/server/placements/cache` (action `server:placement-admin`)
  purges the cache.
- IDs have 20 random characters, so a caller who does not know an ID cannot
  find one by guessing. The rate limit bounds the traffic that random IDs
  cause.
- Rejected: a placement hint in the ID. A namespace moves between
  installations (ADR 47 decision 6), so a hint goes stale. Shopify, Slack and
  Vitess keep the shard out of the ID for this reason. See question A-2.

### A3. How a forward works

- `net/http/httputil.ReverseProxy` with `Rewrite`, not `Director`. Bodies
  stream and are never buffered. The forwarded bytes count against the
  bound on bodies in flight.
- The router removes `Dustpan-*`, `Forwarded` and `X-Forwarded-*` from each
  request that does not come from a peer, before it reads them. A test sends
  `Connection: Dustpan-Forwarded-By` to check that a hop-by-hop header cannot
  remove a header that the router adds.
- One hop. The router adds `Dustpan-Forwarded-By: <installation name>` and
  `Dustpan-Placement-Version: <failover version>`. A request that has the
  first header is never forwarded again, so two maps that disagree give an
  error, not a loop.
- Mutual TLS between peers. A peer accepts the forward headers only from a
  client certificate in its peer list.
- **Tokens (ADR 248 decision 13).** The token of the caller passes
  unchanged. The primary authenticates and authorizes again. The router
  never adds rights. A forward works for two kinds of token: a token of a
  namespace principal (it replicates with the namespace, ADR 215), and a
  token that the primary installation issued. The router does not forward a
  token that its own installation issued for an installation principal,
  because the primary refuses it. It answers 421 with the public URL of the
  primary instead. The slice 7 e2e step names the token kind of each case.
- **A peer sees each token that it forwards.** A peer that an attacker
  controls can replay those tokens against each installation that holds the
  namespace. So a peer is trusted with the tokens of the namespaces it
  forwards. The docs say this, and the peer list is in the static layer
  only.
- **The client address.** The router passes the client address in a header.
  The primary uses it for the audit entry and the logs only, marked as
  given by the peer. It never uses it for an access decision: not for the IP
  allowlist of a namespace (`ipallow`) and not for the sign-in failure
  windows. Those see the address of the peer. So a namespace with an IP
  allowlist that wants forwarding lists the address of each peer, and with
  that it trusts the peer. The forwarder also checks the allowlist on the
  real client address when it holds a copy of the namespace. A test sends a
  spoofed client address from a peer and checks that the allowlist decision
  does not change.
- Absolute URLs (upload URLs, discovery): the router passes
  `Dustpan-Front-Door: <public URL>`. The primary uses it only when it comes
  from a peer and the value is a public URL in its peer list. Otherwise it
  uses its own public URL.
- **Answers.**
  - The primary does not answer (connect error, timeout): 503 with
    `Retry-After` and the name of the primary installation. This is the
    answer of ADR 280 decision 4, which the CLIs already retry.
  - **During a cutover**: a forwarded request reaches an installation that
    is not the primary, but is in handover, or has a lower failover version
    than `Dustpan-Placement-Version`. It answers 503 with `Retry-After`,
    never 421. The forwarder and the target then agree after the lag.
  - Forwarding off, or a request that did not come through a forward: 421
    in each adapter, with the name and the public URL of the primary in the
    error text. The runner API changes from 503 to the same rule, but keeps
    503 during a handover. The runner client treats 421 as "register with
    the primary installation in the answer", never as a stop.
- No redirect. The Go HTTP client drops the `Authorization` header on a
  redirect to another host, so a 307 reaches the primary with no token.

### A4. The installation scope on a replica

The installation scope of a replica installation (installation
administrators, temporary administrators, break-glass accounts, installation
roles and groups) is not the installation scope of the primary. The owners
of the namespace never granted it.

- **Proposal: the installation scope of a replica gives no access to a
  replicated namespace.** The authorizer of a replica evaluates only the
  grants that replicate with the namespace. The one exception is the set of
  replication actions on the namespace (`server:replication-*`: read the lag,
  stop, copy again, forced failover), which an installation administrator of
  the replica needs to operate it. Each such action writes an audit entry.
- The other option is that adding a replica gives the administrators of the
  replica that reach, and the docs say so. Question A-3.
- A test gives an installation administrator of the replica a request for a
  unit of a replicated namespace, and checks that the answer is a denial
  with an audit entry.

### A5. The console

A console session is installation scope (ADR 248, "A local installation
scope"). It does not forward. A request of the console for a namespace on
another installation gets 421 with the public URL of the primary. The console
then opens that installation, and the person signs in there. With one
identity provider for each federation, that sign-in needs no new password
(RFC 37 design point 3). This is the Vault model: tokens and sessions are
local.

A replica serves console reads with the token revocations that it holds. A
revocation of a namespace principal or of its grant reaches the replica after
the replication lag. The lag limit of A2 bounds this window.

### A6. Lists across installations

`/api/user/stacks` and the inventory list answer the local installation only.
The answer says that it is local, as ADR 17 says for a list that leaves out
objects. A merged list across peers is a later slice (RFC 37 design point 8).

## Part B. Replication and failover (issues #1051, #1052, #1055, #1056, #1058)

### B1. The mechanism: a log of each namespace, in the application

Options:

1. **PostgreSQL logical replication** with row filters. Rejected for the
   stream: it does not work on SQLite, it does not carry blobs, it cannot
   refuse a change of an old failover version, and 16 columns select the
   namespace by name, not by ID. It stays the right tool for the database of
   one installation (ADR 280, ADR 298).
2. **A geo-distributed database** (CockroachDB, Spanner, YugabyteDB).
   Rejected: each write waits for a quorum, which ADR 248 decision 9
   forbids.
3. **A replication log in the application.** Recommended. Each namespace
   write appends to a log in the same transaction as the write and the fence
   check. This is the transactional outbox. The inventory change feed is the
   same shape already.

The log:

- `replication_log (namespace_id, failover_version, seq, at_ms, kind,
  table_name, row_key, row_image, blob_key, blob_sha256)`. `seq` comes from
  a head row for each namespace, locked in the write transaction, as
  `inventory_change_heads` does, so commit order equals `seq` order and the
  log has no gaps. `row_image` is the row after the change, in the portable
  format of ADR 42. A delete is an entry with no image. A blob entry names a
  blob under `namespaces/<namespace ID>/` and its SHA-256.
- Only the primary appends. The store appends in the transaction that checks
  the fence, so no write can skip the log.
- **Blobs.** A blob is written before the row that names it (the rule of
  today). The replica fetches each blob of a batch before it applies the
  rows of that batch, and checks the SHA-256, so a replica never holds a row
  whose blob is missing or changed. Blobs never change (ADR 217 decision
  18). The log carries the key and the digest, never the bytes.
- **Blob deletes wait for replicas.** A deleter on the primary (retention,
  purge, shred) deletes a blob only when each replica cursor has passed the
  entry that deleted or replaced the row. This is the rule of ADR 280
  decision 7 for backups, with the replica cursors as a second reader. A
  replica that finds a blob missing anyway stops and copies again. A replica
  that the operator removed no longer holds back deletes.

The protocol:

- The replica pulls. `GET /replication/v1/namespaces/{id}/changes?after=<failover version>:<seq>`
  over mutual TLS, with a long poll. Pull, not push, because the replica
  owns its cursor, and a primary never waits for a replica (ADR 248 decision
  9).
- **The handshake** carries the protocol version of the stream, the
  SHA-256 of the schema baseline of each side (B8), and the installation
  name and index of each side. A mismatch of the protocol or the baseline
  stops the stream and fails closed. So does an index that differs from the
  index of that peer in the local peer list, or that equals the local index,
  because two peer lists can disagree.
- The replica applies one batch in one transaction, in order, with its
  cursor `(failover version, seq)` in the same transaction. After each
  batch, the `seq` head row of the namespace on the replica equals its
  cursor, so a replica that becomes primary continues at `fork_seq + 1`.
- The apply is the one write path that skips the primary check of door 3,
  through its own named store method, `ApplyReplicated`. Its input comes
  from outside the process, so it has its own layers (B1a).

### B1a. The checks of `ApplyReplicated`

A peer stream is untrusted input. A source that an attacker controls, or a
source with a wrong configuration, can send a row of an `installation`
table, a row of another namespace, or an `access_tokens` row of an
installation principal of the replica, which would give an installation
administrator token on the replica. Each layer below stops these alone. Each
has its own test that sends a hostile entry straight to that layer.

1. **The source.** In the apply transaction: the local namespace row does
   not name this installation as primary. The client certificate of the
   source is the peer that the local row or the verified history names as
   primary. The version of the batch is not lower than the highest version
   that this installation knows from any source, including the placement
   read (A1). So a replica that learns of a newer primary stops applying the
   old source, and waits for the `source` route (B5 step 3).
2. **The table.** The table of each entry is in the class list with class
   `namespace`, `child` or `mixed`. Never `installation`, and never a table
   that is not in the list.
3. **The row.** The `namespace_id` of the row, its foreign key path (class
   `child`) or its predicate (class `mixed`) selects the namespace of the
   stream. The replica evaluates the predicate again on its own data. Each
   class can add a rule. Examples: an `access_tokens` row or a `grants` row
   arrives only for a principal whose home is the namespace of the stream,
   and the replica checks the principal row that it already holds for that
   namespace; an entry never writes the `primary_installation` or the
   failover version of the namespace row, except a failover entry that B3
   verifies.
4. **The blob.** Each blob key is under `namespaces/<namespace ID>/` of the
   stream, through `blob.Scoped`, and the SHA-256 of the fetched bytes
   matches the entry.
5. **Fail closed.** A refused entry rolls back its batch, stops the stream,
   marks the namespace `replication-refused` with the layer and the entry,
   and writes an audit entry. The replica never skips an entry and goes on.

Defense in depth: layers 2 and 3 stay even when the source passed layer 1,
so that a trusted source with a fault cannot write outside the namespace.
- **Copy, then stream.** A new replica first restores a namespace backup
  (ADR 218) taken at a known `(failover version, seq)`, then pulls from that
  position. That is the pattern of Ghostferry, Vitess and PostgreSQL logical
  replication.
- The primary keeps the log until each replica has read it, up to a
  retention limit. A replica that falls behind the limit copies the
  namespace again. The primary reports the lag of each replica, in entries
  and in seconds.

### B2. The class of each table (issue #1051)

Each table has one class, in one list in code. The backup already keeps such
a list (`pkg/backup/portable_tables.go`). This design reuses it.

| Class | Rule | Examples from `0168_baseline.sql` |
| --- | --- | --- |
| `namespace` | Has `namespace_id`. Replicates. | `stacks`, `workspaces`, `grants`, `collections`, `runner_pools` |
| `child` | Has no namespace column. Reaches the namespace through a foreign key. The list names the path. Replicates with its parent. | `checkpoints`, `updates`, `update_events`, `update_journal*`, `state_versions`, `stack_tags`, `workspace_blame_*`, `workspace_resource_changes`, `group_members` |
| `mixed` | Holds rows of both scopes. The list names the predicate that selects the rows of a namespace. Those rows replicate, and the others stay local. | `access_tokens` (tokens of a namespace principal replicate, tokens of an installation principal do not), `idempotency_records` (records of a namespace route replicate), `durable_operations` (operations of a namespace), `notifications` and `notification_sequences` (`scope`), `name_locks` (`scope`) |
| `installation` | Local. Never replicates. | `sessions`, `instance_versions`, `roles`, `maintenance_revocations`, `audit_chain_state`, runner sessions and claims |

- A test reads each table of the baseline and fails when a table has no
  class.
- A second test reads each column whose name holds `namespace` or `scope`,
  and fails when the column is not the key of the class or its predicate.
- A `mixed` table may later split into two tables. Slice 0 decides each one
  with the code.

### B3. The failover history, the cursor and the failover version

- **The failover version takes the Temporal index form.** Each installation
  in a peer list has an index from 1 to 99. A new failover version is the
  next number above the highest version that the installation knows whose
  last two digits are its index. Two installations can never make the same
  version. A server with no peer list has index 0, so a restore or a
  failover raises the version by steps of 100 from the same base. The rule
  "the highest version wins" is then total, for versions that pass the
  proof below. The pair rule of the first draft (`(version, primary)` for
  equal versions) is withdrawn.
- **Which index a version carries.** The index of the new primary, as in
  Temporal. At a planned failover the old primary makes the version with the
  index of the target from its peer list. At a forced failover and at a
  restore, the installation that becomes primary makes it with its own
  index.
- **The failover history.** Each namespace has a table
  `namespace_failovers (namespace_id, failover_version, primary_installation,
  fork_seq, kind, at_ms, actor, reason, prev_row_sha256, signer,
  signing_key_id, signature)`. `fork_seq` is the last `seq` of the earlier
  version that the new version contains. `prev_row_sha256` chains each row to
  the row before it, so one valid signature on a row also covers the history
  before it. Each failover, each restore and each forced failover appends a
  row, and the row replicates in the log like any other row. A primary that
  adds a replica appends a row of the kind `replica-added`, and one of the
  kind `replica-removed` when it removes it. So the history names each
  replica of record.
- **Proof of a version.** A bare number from a peer proves nothing. One peer
  that an attacker controls could otherwise draw forwards and their tokens
  to itself, make a returning primary refuse writes, and block each restore.
  So each history row is signed with the signing key of the installation
  that wrote it (Ed25519; the public key is in its peer entry, next to the
  transport key of B7). An installation accepts a history row, from its
  stream or from the placement read, only when:
  - the last two digits of the version are the index of the installation
    that the row names as primary, in the local peer list;
  - the signer is that new primary (forced failover, restore), or the
    primary of the previous history row (planned failover); and
  - the signature over the namespace ID, the version, the primary, the
    `fork_seq`, the kind, `prev_row_sha256` and `signing_key_id` is valid,
    with the key of that ID in the local peer entry of the signer, under the
    rules of B3a; and
  - for a forced failover, the signer is a replica of record in the verified
    history (residual risk, B3a).

  Each refused claim writes an audit entry with the peer. The placement
  read answers the signed history rows, never only a number.
- **No divergence on a claim alone.** An installation marks a namespace
  `diverged` only on a history row that passes the proof. A row signed by the
  new primary is the confirmation of the new primary.

### B3a. The signing key of the failover history

**Custody.** The private signing key of an installation stays on that
installation. It is in the static layer as a key file that only the server
process can read, or it is a key in a KMS that only the role of the server
can use for a signature. It is never in a database row, a replicated row, a
namespace backup or an installation backup. The server uses it only to sign
history rows. The operator who can change the static layer rotates it. No
API route reads it or changes it.

**The key ID.** Each key has an ID: the first 16 bytes of the SHA-256 of the
public key, in hex. Each history row holds `signing_key_id`, and the
signature covers it.

**The peer entry** of each installation holds:
- `current`: the key that the installation signs with now;
- `retired`: earlier keys, each with the time it was retired;
- `revoked`: keys that must not be trusted, each with the time of
  revocation in the local peer list.

**Rules for a row that arrives** (in a stream, in a copy or from the placement
read):
- A current key verifies it.
- A retired key verifies a row that the key signed. A retired key is
  retired, not doubted, so old rows still verify after a rotation, and
  replication of those namespaces goes on.
- A revoked key verifies no row that arrives after the time of revocation in
  the local list, whatever time the row states, because the holder of a
  stolen key can state any time. A row that this installation accepted
  before that time stays accepted, because it is already stored and its hash
  is in the chain.

**Rotation**, with an overlap, as for the transport key (B7):
1. The installation makes a new key pair, and the operator adds the public
   key to its peer entry in each peer as `current`, and moves the old key to
   `retired`.
2. When each peer has the new entry, the installation signs with the new key.
3. The old key stays in `retired` as long as any history row it signed
   exists. Each step writes an audit entry.

**After a compromise:**
1. The operator moves the key ID to `revoked` in the peer entry of that
   installation, in each peer. Each peer writes an audit entry.
2. The installation makes a new key and adds it as `current`, as in a
   rotation.
3. For each namespace whose history holds a row signed by the revoked key,
   the primary installation of the namespace appends a row of the kind
   `re-attest`, signed with a key that is not revoked. Its `prev_row_sha256`
   covers the history before it. A copy or a new replica then accepts the
   history through the chain of that row. When the primary is the
   installation whose key was revoked, it signs the `re-attest` row with its
   new key after step 2.
4. Each history row that a peer refused in the meantime is in its audit log.
   The operator reviews each one. A forced failover that the stolen key
   signed and that a peer accepted before step 1 is a failover that the
   operator undoes with a planned or forced failover of their own.

**Residual risk.** A peer with a valid key can still sign a forced failover
that names itself as primary. The proof shows who signed, not that an
operator wanted it. These rules bound it:
- a peer accepts a forced failover only from a replica of record in the
  verified history, so an installation that never held a copy cannot claim
  the namespace;
- each accepted forced failover writes an audit entry and an alert on each
  peer, so the operators of the namespace see it at once;
- the row holds the actor and the reason of the failover record of the
  operator on the new primary.

A stronger bound is that each peer accepts a forced failover only after its
own operator confirms it. That adds an operator step on each peer, but no
write waits for it. It is an option in question B-3. A quorum of peers is
not proposed, because ADR 248 decision 9 rules out a quorum.
- **The cursor** of a replica is `(failover version, seq)` of the last
  entry that it applied.
- **Divergence.** A replica that changes its source, or that reads a
  failover entry, compares its cursor with the history of the source: when
  its cursor is at version `v` and at a `seq` above the `fork_seq` of the
  next version after `v`, it holds entries that the new primary does not
  have. It marks the namespace `diverged`, stops, keeps its copy readable
  for an export, and copies again. This is the rule of PostgreSQL timeline
  history and of the Kafka leader epoch, without truncation in place,
  because a row image cannot be undone.
- So divergence is found on each copy, not only on the old primary. Example:
  A is primary, B and C are replicas. A writes `seq` 101 to 110 at version
  `n`. C applies them, B applies up to 100. An operator forces B. B writes
  history `(n', B, fork_seq 100)`. C follows B, compares its cursor
  `(n, 110)` with `fork_seq 100`, and finds that it diverged.
- **Restores in a federation.**
  - A restore never raises the failover version of a namespace for which
    this installation is not the primary installation.
  - A restore on a replica marks each replicated namespace for a fresh
    copy. The replica never applies its restored rows of a replicated
    namespace.
  - Before a restore on a primary raises a version, it reads the placement
    from each peer (A1). It refuses a namespace for which a peer shows a
    higher version that passes the proof. A peer that does not answer makes the restore of that
    namespace need an explicit operator flag, as a forced failover does.
  - A restore on a primary appends a history row with `fork_seq` set to the
    `seq` in the backup manifest. Replicas past that `seq` find that they
    diverged, and copy again.

### B4. Planned failover

The Temporal handover, with the wait before the switch:

1. The operator calls `POST /api/server/namespaces/{id}/failover` with the
   target replica, on the primary. The failover is a durable operation
   (ADR 72) with a deadline, so it survives the stop of the instance that
   took the request.
2. **Drain.** The primary sets the namespace to `handover` in the namespace
   row, in a transaction. The store checks the state in the write
   transaction, not only in the HTTP layer. Each write answers 503 with
   `Retry-After`, and each CLI retries. The primary waits until the cursor
   of the target is at the head of the log. A timeout in this phase clears
   `handover` and aborts. Nothing has switched, so the abort is safe. When
   the deadline passes and the switch did not commit, any instance that
   claims the durable operation clears `handover`. So a stop during the
   drain never leaves writes paused.
3. **Switch.** In one transaction, with the namespace row held
   `FOR UPDATE`, the primary appends the failover entry and the history row
   `(new version, target, fork_seq = head)`, and writes its own namespace row
   with the new version and the target as primary. Each write in flight
   then gets `ErrFenced` or `ErrNotHome`. After this step there is no abort.
   When the target never applies the entry, the only way on is a forced
   failover.
4. The target applies the entry. Its row then names itself as primary, with
   the new version. It accepts writes.
5. The old primary is now a replica of the target. Forwarding goes to the
   target. Between steps 3 and 4, a forward to the target answers 503 (A3,
   "During a cutover").

No change is lost. Writes pause for the replication lag at step 2,
typically seconds.

### B5. Forced failover and the divergent tail

0. **Fence the old primary first.** A removal from the peer lists alone is
   not a fence: CLIs that reach the old primary through DNS still write to
   it, and those writes are lost. So the operator either stops the old
   primary, or cuts it off from clients. ADR 248 asks for this in its
   consequences. The load balancer or a firewall is the better way to cut
   it off. A change of DNS reaches a client only when the time to live of
   the record ends, so with DNS the operator keeps the old primary isolated
   for at least that time, and states it. The failover route needs the field
   `oldPrimaryFenced` with the value `stopped` or `isolated-from-clients`,
   and the audit entry records it. The server cannot check it, so this is
   the statement of the operator. Question B-3.

   The operator also moves the old primary from the peer list of each other
   installation to its list of **fenced peers**. The list of fenced peers is
   in the static layer, next to the peer list, so the operator who can change
   the static layer of an installation changes it. The API shows it, and
   cannot change it. A fenced peer:
   - gets no forwards, and its forwards are refused;
   - gets no log entries and no wraps of a namespace key (B7);
   - has its tokens refused, also tokens of namespace principals that it
     issued after the version of the failover;
   - can still call one route, the placement read (A1), with its client
     certificate, so that it learns of the new primary when it returns.
1. The operator calls the failover route on the replica with `force: true`
   and a reason. The audit entry names the operator and the reason.
2. The replica writes the history row `(new version, itself, fork_seq = its
   cursor)` and its row as primary, with the new version.
3. **Peers learn of it.** The other replicas read the failover entry when
   they pull from the new primary. The operator points each replica at the
   new primary with `POST /api/server/namespaces/{id}/source`. The placement
   read from peers (A1) tells each installation that does not pull from the
   new primary, and the old primary when it returns, that a higher version
   exists.
4. When the old primary returns, it reads the placement from its peers at
   start, before it accepts writes to replicated namespaces, and then on the
   timer. As a fenced peer it can still read it. A history row with a higher
   version that passes the proof of B3 makes it stop. The entries after
   `fork_seq` are its divergent tail. It marks
   the namespace `diverged`, refuses writes, and keeps the tail readable for
   an export (ADR 248 decision 11). An operator then makes it a replica again
   from a fresh copy.

The placement read is not a lease. A write never waits for it. An old primary
that cannot reach any peer keeps accepting writes, as ADR 248 decision 9
says, until the operator fences it (step 0).

### B6. Work in flight at a failover

| Work | At a planned failover | At a forced failover |
| --- | --- | --- |
| Idempotency records of namespace routes | Replicate (class `mixed`). A retry with the same `Idempotency-Key` on the new primary gets the stored answer. | Replicate up to `fork_seq`. A retry of a request in the divergent tail runs again. The docs state this. |
| Durable operations of the namespace | Replicate. The lease of ADR 72 is local, so the new primary claims each open operation after the lease period. Each step checks the fence, so a step of the old primary fails. | The same, from `fork_seq`. |
| Schedules, drift checks, retention | Rows replicate. They run only on the primary (ADR 248 decision 14). The new primary runs the next due time. A run that was due during the handover runs once, late. | The same. A run in the divergent tail may run again. |
| Pulumi updates and OpenTofu locks | The update, its lease token and the lock replicate. A CLI in the middle of `pulumi up` gets 503 during the handover, retries, and continues on the new primary with the same lease token. | An update in the divergent tail is lost. Its lease ends on the new primary, and the CLI reports the update as failed. |
| Runner jobs | The job rows of the namespace replicate, with the phase `running`, the claim ID and the failover version of the claim. Runner sessions stay local. The new primary never sends a job in `running` to a runner again. It waits for the runner of that claim: the runner gets 421 with the new primary, registers there, and reports on its claim ID, and the new primary accepts the report. A claim of an older failover version that no runner reports on within the claim timeout moves the job to `unknown`, which needs an operator decision. The server never sends it again on its own, because the first runner may still change real infrastructure. A report to the old primary gets `ErrFenced`. | The same, for claims up to `fork_seq`. A claim in the divergent tail is not on the new primary. Its runner reports there, the claim ID is unknown, and the job gets the state `unknown` on the new primary when the job row exists, or an audit entry when it does not. |
| Notifications | The notification rows replicate. Each notification has a stable ID. A notification sent near the switch can be sent again by the new primary. Delivery stays at least once, and a receiver deduplicates by the ID. | The same. |
| Audit chain | The entries of the namespace chain replicate (class `namespace`). `audit_chain_state` is the local state of the chain process of an installation, not the head. The new primary takes the head of the namespace chain from the last replicated entry of the namespace, its sequence and its hash, and continues from there. Each entry holds its failover version (ADR 64 decision 10), so a verifier sees the switch. | The chain forks at `fork_seq`. The verifier shows the fork. |

A replica records the reads and the denials that it serves in its own
`@server` audit scope, with the namespace ID in the entry, because it cannot
append to the chain of the namespace.

An e2e step (slice 5) runs `pulumi up` and `tofu apply` through a planned
failover, with a retried `Idempotency-Key` request, an open durable
operation and a runner job claimed on the old primary, and checks that each
completes once and that no second runner gets the job.

### B7. Keys (issue #1055)

ADR 217: one namespace key wraps the data keys and item keys of the
namespace. Each installation has its own key-encryption key.

- Data keys and item keys replicate as stored, because the namespace key
  wraps them.
- The namespace key cannot replicate as stored, because the key-encryption
  key of the primary wraps it. Each peer entry holds the public transport key
  of the peer (A1), so the static layer binds the key to the peer identity.
  The primary sends each namespace key version wrapped to that key with HPKE
  (RFC 9180). The HPKE `info` binds the label
  `opendustpan/namespace-key/v1`, the namespace ID, the key version, the
  sender installation and the recipient installation. The replica unwraps
  it in memory and wraps it again with its own key-encryption key.
- **A difference from the text of ADR 248.** ADR 248 "Not decided" proposes
  one namespace key record with one wrap for each installation. This RFC
  proposes a local record on each installation, with one wrap. A record that
  holds the wraps of other installations would replicate wraps that no
  local key can open. The owner decides with issue #1055.
- **Transport key rotation.** An operator adds the new key to the peer entry
  next to the old one. The primary wraps to the newest key. The old key
  leaves the entry after each namespace key version is sent again.
- **Revocation of a replica.** When an operator removes a replica, or a
  replica is compromised:
  1. The operator moves it from the peer list to the fenced peers (B5), so
     it gets no more log entries and no more wraps, and its forwards and
     tokens are refused.
  2. The primary rotates the namespace key and wraps each data key and item
     key again under the new version. The removed replica never gets the new
     version.
  3. The removed replica still holds the old key versions and every value it
     already copied. Where the threat model needs it, an operator also
     rotates the data keys, which encrypts the values again.
- **Shred.** A shred deletes the item key on each copy (ADR 151). The
  primary records the cursor at which each replica applied the shred
  entry. The shred is complete only when each copy applied it: each
  replica, a source that a move keeps for its retention period, and a
  divergent tail. Until then, the shred status names each copy that is
  pending.
- **The end state for a copy that never applies.** A removed or fenced
  replica, a compromised one, and a `diverged` copy that stopped never apply
  the shred entry. For such a copy, an operator records its destruction in
  two steps, in this order:
  1. the destruction of its keys: its key-encryption key, and with it each
     namespace key version that it held;
  2. the destruction of its data: the database and the blobs of the copy.

  Each step is one call with the copy, the method and a reason, and writes
  an audit entry with the actor. The server cannot check a statement about
  an installation it cannot reach, so the status shows it as a statement:
  "complete, except copies stated destroyed by <actor> on <date>". A key
  destruction that a KMS reports (for example a scheduled key deletion) can
  be attached as evidence. The same record closes the namespace key
  versions that a removed replica held (the revocation above).
- A customer key (ADR 217) must be reachable from each installation that
  holds the namespace. The docs name multi-Region KMS keys as the way.

### B8. Schema version skew

The log carries row images of the schema of the primary. Before 1.0 there
is no conversion code (CLAUDE.md).

- The pull handshake carries the protocol version of the stream, the
  SHA-256 of the schema baseline of each side and the installation index of
  each side (B1).
- A mismatch stops the stream, fails closed and reports the lag and the two
  versions. Replication waits until both sides run the same version.
- So until the first tag, every installation of a federation runs the same
  version of the server, and an upgrade pauses replication. Question B-4.
- After the first tag, the upgrade order is replicas first, then the
  primary. A replica refuses an entry with a column it does not know. The
  rule for each later change of the log format is part of the upgrade path
  of ADR 72.

### B9. The discovery document and signing keys (issue #1056)

- Each installation that holds the namespace answers the discovery document
  and the key set from its copy. Only the primary signs.
- The issuer URL never changes (ADR 204 decision 1). So a namespace that may
  ever replicate or move needs an issuer base URL that the operator can
  move to another installation, for example a DNS name of the federation.
  This is a one-way door for each namespace, not for the code. The docs say
  it before the first namespace is made.
- ADR 248 decision 12 decides the keys at a forced failover.

### B10. Joins between namespaces (issue #1058)

The two halves of RFC 79 section 8, as ADR 248 lists them. Consul cluster
peering uses the same pattern: each side exports or accepts on its own. No
change.

## Part C. Moves (RFC 66)

### C1. Between namespaces: decided

ADR 346: no one-click move. Export and import (ADR 241) with the move check
(ADR 244).

### C2. Between collections: what the list of effects covers

ADR 346 leaves open which effects the list covers and whether the server
refuses a move that breaks a grant.

- **The effects.** One port, `move.EffectProvider`, that each domain
  implements: access (grants on the source and target collections),
  profiles (bound to the collection), output sharing rules, saved views that
  filter by collection, placement rules, run triggers, gates and approval
  rules with a collection scope, manual locks of a collection. A test fails
  when a domain that stores a collection reference has no provider. This is
  the same rule as the carry-over findings of ADR 244, which a new feature
  must extend.
- **No refusal, but a confirm digest.** The move endpoint first answers the
  list with a digest of it. The client sends the digest back with the
  confirm. The server computes the list again in the transaction of the
  move, and refuses the move when the digest changed. So a person never
  confirms a list that is no longer true. Terraform and OpenTofu apply a
  saved plan in the same way.

### C3. A namespace between installations

Two ways, for two setups:

1. **With federation: a move is a replica, then a planned failover, then a
   detach.** Add the target as a replica (copy, then stream), wait until the
   lag is small, run the planned failover of B4, then remove the old
   installation as a replica (with the revocation of B7) and delete its copy
   after a retention period. This is the Shopify pod move and the Vitess
   `MoveTables` flow. It loses no change, and writes pause for seconds. It
   needs no code beyond Part B.
2. **Without federation: an offline move.**
   1. A manual lock on the namespace (ADR 213) stops new work.
   2. A namespace backup (ADR 218) to a backup target that both
      installations can read.
   3. A restore into the target installation that keeps the namespace ID and
      the name, sets this installation as primary, and raises the failover
      version (B3). ADR 218 restores only into its own namespace on its own
      installation today. This needs a "restore as a move" mode.
   4. The source keeps a moved marker: its namespace row names the target as
      primary, with the new version. Each request then gets 421 with the
      target, or a forward.
   5. The operator changes DNS or the placement entry. The source deletes
      the data of the namespace after a retention period.

**The moved marker stays.** After the source deletes the data, it keeps the
namespace row as a marker, with no data, so the old URL still answers 421 with
the target and not 404. An operator can delete the marker. Then the old URL
answers 404 with no pointer. The marker is one row, so the proposal keeps it
until an operator deletes it.

Both ways keep each grant, profile and setting inside the namespace,
because the namespace moves whole. They break only references to the
installation scope. The move check of a whole namespace (ADR 244 decision 1)
adds these findings:

| Code | Severity | When |
| --- | --- | --- |
| `carry.installation-principal` | warning | a grant names a principal or group of the installation, which does not move |
| `carry.installation-role` | warning | a grant uses a custom role of the installation |
| `carry.idp-group-mapping` | error | a grant depends on a group mapping of the sign-in provider of the installation. On the target, the same group name can hold other people, so access can widen. The person maps the group on the target before the move. |
| `carry.forge-connection` | warning | a unit uses a git host connection with the installation scope (ADR 271) |
| `carry.git-host-app` | warning | a connection uses a git host app registration of the installation (`git_host_app_registrations`) |
| `carry.runner-registration` | warning | a runner pool of the namespace moves, but its runners registered with the source installation and must register again with the target |
| `carry.notification-destination` | warning | a rule sends to a notification sink with the installation scope |
| `carry.saved-view` | warning | a saved view of a person of the installation scope filters on the namespace |
| `carry.idempotency` | warning | the offline move does not carry idempotency records of requests in flight. A retry after the move runs again. |
| `carry.issuer-url` | error | the issuer base URL of the namespace does not resolve to the target (B9) |

### C4. A unit between installations

The same as between namespaces: export and import, with the move check. No
new mechanism.

## One-way doors before the first tag

Before the first tag, the baseline can change in place (ADR 322). After it,
each item below needs a migration with a data conversion, or cannot change.

| # | Door | State on `main` | Proposal |
| --- | --- | --- | --- |
| 1 | The associated data of each sealed secret | Binds to the namespace **name** (`pkg/secret/secret.go`, `Scope.aad`). | Bind to the namespace **ID**. A ciphertext that shipped with the name can never be read under another name without a rewrap of each value. The issuer path already uses the ID (ADR 204). |
| 2 | Namespace references in rows | 16 columns hold the namespace name, not the ID: the `namespace` column of `audit_entries`, `audit_sink_cursors`, `collection_members`, `collections`, `grants`, `inventory`, `inventory_deleted`, `inventory_labels`, `manual_locks`, `placement_rules`, `principals`, `secret_keys`, `stacks`, `trust_rules` and `workspaces`, and `triggers.source_namespace`. The `scope` columns of `notifications`, `notification_sequences` and `name_locks` hold a name or `@server`. | Key each one by the namespace ID. A replica that must refuse a name conflict, or a future rename, then touches one row. The test of B2 reads each column whose name holds `namespace` or `scope`. |
| 3 | The fence | `Fence{Namespace Name, FailoverVersion}`. The store checks only the version. Fence sources such as `namespaceFences.Fence` (gates) and `jobFences.Fence` (jobs) read the namespace with `Get`. | Carry the namespace ID. In the write transaction, the store checks the version **and** that `primary_installation` is this installation, and answers `ErrNotHome` otherwise. Every fence source uses `WritableHere`. Each port gets a conformance test that writes with a fence of a namespace whose primary is another installation. The replication apply is the one path that skips the check, through `ApplyReplicated` (B1). Defense in depth: the store check stays even where the fence source already checked, so that a new caller cannot skip it. |
| 4 | IDs | Random, 20 characters, no placement. | Keep. Never add an installation or a region to an ID. A test checks that `ident.NewID` holds no setting. |
| 5 | The class of each table | Implicit. | The four classes of B2, with the tests of B2. A namespace row may name a row of the installation scope only by its stable ID, and must work when that ID is missing on a replica. |
| 6 | Blob keys | Under `namespaces/<namespace ID>/`, with `blob.Scoped`. | Keep. A conformance test checks that each namespace blob uses the scoped view. |
| 7 | The failover version | `int64`, raised by one at each restore. | The index form of B3: monotonic, never reset, and the last two digits name the new primary installation of that version. With no peer list the step is 100. Each history row holds `prev_row_sha256`, `signer`, `signing_key_id` and `signature`, and is signed by the installation that wrote it (B3a). |
| 8 | Order between installations | No code depends on it today. | Order across installations is `(failover version, seq)`, never a wall clock. Timestamps stay data. |
| 9 | The issuer base URL | Per installation. | Docs: a namespace that may move needs a movable host name before it issues its first token (B9). |
| 10 | Rows of `mixed` tables | `access_tokens` holds `principal_id`; `idempotency_records` holds the principal and the path; `durable_operations` holds no namespace column. | Each row of a `mixed` table names its namespace ID, or an empty value for the installation scope, in a column of its own, so that the predicate of B1a layer 3 needs no parse of a path or an input. |

Items 1, 2, 3, 7 and 10 are the real doors. Item 7 also adds the signer
and the signature to the failover history (B3), which no tag shipped yet. They are cheap now: an edit of the
baseline in each dialect, a change of the fence check, a `make generate`, and
a `dev reset`.

## Slices

Each slice lands with its e2e step first (CLAUDE.md, "A shared core, one
adapter per tool"). Slices 0 and 1 can land before the first tag. The others
are "Later" (ADR 280).

| Slice | Scope | e2e step |
| --- | --- | --- |
| 0 | One-way doors 1 to 7 and 10: the ID in the secret scope, the fence and the rows; the primary check in the fence transaction and `WritableHere` in each fence source; the table classes and their tests; the index form of the failover version; a namespace column in each `mixed` table. | The suites of today pass. A write through each fence source with a namespace row whose primary is another installation gives `ErrNotHome`. A restore of a backup made before the change is refused with `ErrSchemaDiverged` (ADR 322). |
| 1 | One answer for `ErrNotHome`: 421 in each adapter, with the primary and its public URL. | Two installations on one database copy: a write to the replica gives 421 from `pulumi up`, `tofu apply` (`cloud` block) and the http backend, with the primary in the text. |
| 2 | The namespace backup (ADR 218), with `(failover version, seq)` of the log in the manifest. | Back up one namespace, restore it into a fresh installation, and `pulumi stack export` and `tofu state pull` give the same bytes. |
| 3 | The replication log, the failover history, the pull protocol with the handshake, and blob deletes that wait for replicas; the checks of `ApplyReplicated` (B1a); replicas read only. | Primary and replica: `pulumi up` on the primary, then `pulumi stack export` against the replica gives the same deployment after the lag. The same for `tofu apply` and `tofu state pull`. A write at the replica gives 421. A replica on another schema version stops and reports it. A test source sends, one at a time, a row of an `installation` table, a row of another namespace, an `access_tokens` row of an installation principal of the replica, a blob key outside the namespace, and an entry from a peer that is not the primary; the replica refuses each one, stops the stream and writes an audit entry. |
| 4 | Keys over the stream, transport key rotation, and the revocation of a replica (B7). | A secret config value set on the primary decrypts on the replica with `pulumi config get --show-secrets` after a planned failover. A removed replica gets no new namespace key version. |
| 5 | Planned failover (B4) and work in flight (B6). | `pulumi up` runs in a loop during the failover. Each update succeeds after retries, and no checkpoint is lost. The same with `tofu apply`. A request retried with the same `Idempotency-Key` across the switch runs once. A forward during the switch gives 503, never 421. A runner job claimed on the old primary completes once, and no second runner gets it. A stop of the primary during the drain clears `handover` at the deadline. |
| 6 | Forced failover, the fence step, the placement read from peers, and divergence on each copy (B3, B5). | Three installations. Cut the link, write on A, let C apply more of A's tail than B, force B, restore the links. A and C each mark the namespace `diverged`, and A refuses writes. The tails export. A, moved to the fenced peers, can read the placement but its forwards and tokens are refused. C, still connected to A during the cut, stops applying A's entries once it learns of B's version. A peer that sends a history row with a forged version or signature is refused and audited, and no namespace becomes `diverged` from it. |
| 7 | Request forwarding (Part A), with the rules for tokens, client addresses and the installation scope on a replica. | Each CLI points at the replica. `pulumi up`, `tofu apply` and the http backend succeed through the forward, with uploads, with a token of a namespace principal. A token of an installation principal of the replica gives 421. A stop of the primary gives 503 with `Retry-After`, and the CLI retries. An installation administrator of the replica is denied a unit of the replicated namespace. |
| 8 | The move of a namespace: online (C3 way 1) and offline (C3 way 2), the moved marker, and the new carry-over findings. | Move a namespace with both CLIs in a loop: no failed run after retries, and the move check shows each `carry.*` finding. The old URL answers 421 with the target after the source deletes the data. |
| 9 | The list of effects for a move between collections, and the confirm digest (C2). | API e2e: a move with a stale digest is refused; a move with a fresh digest succeeds; the grant effects match. |

## Questions for the owner

### A-1. Do the console and the tool CLIs both forward?

ELI5: when you knock on the wrong door, either the house walks your letter
next door for you, or it tells you the right address.

- Option 1: forward the tool APIs. The console gets 421 and opens the primary.
- Option 2: forward both. The console then needs a signed assertion between
  installations for its session.
- **Recommendation: option 1.** Sessions stay local, as in Vault. A CLI
  cannot change its URL, a console can.

### A-2. Can an ID carry a placement hint?

ELI5: write the name of the house on each letter, or keep a list of who lives
where.

- Option 1: no hint. IDs stay random. A miss asks the peers, after a rate
  limit, and the namespace ID in the answer is cached for ever, because a
  unit never changes namespace.
- Option 2: an installation code in each new ID.
- **Recommendation: option 1.** A namespace moves, so the hint goes stale.

### A-3. Do the administrators of a replica installation get access to the namespaces it copies?

A replica installation holds a copy of a namespace whose primary is another
installation. The replica has its own installation administrators,
temporary administrators, break-glass accounts and installation roles. The
owners of the namespace never granted these people anything.

ELI5: your neighbor keeps a spare copy of your diary in case your house burns
down. Can the neighbor's landlord read it?

- Option 1: no. On a replica, the grants of the installation scope give no
  access to a copied namespace. The one exception is a short list of
  replication actions that an administrator of the replica needs to run it
  (read the lag, stop the copy, copy again, forced failover). Each one writes
  an audit entry.
- Option 2: yes, and the docs say that adding a replica gives the
  administrators of that installation access to the copied namespaces.
- **Recommendation: option 1.** The owners of the namespace never granted
  that access, and a replica should not widen it.

### B-1. Is replication a log in the application?

ELI5: the primary keeps a diary of each change, and the copies read the diary.
The other way is to buy a special database that copies for you, but it makes
each write wait for a vote.

- Option 1: a log in the application, the same on SQLite and PostgreSQL.
- Option 2: PostgreSQL logical replication, PostgreSQL only, blobs apart.
- **Recommendation: option 1.**

### B-2. Do four storage changes that are cheap now and expensive after the first tag land before it?

The changes are:
- encrypted secrets are bound to the namespace name, and should be bound to
  the namespace ID;
- 16 database columns name a namespace by its name, and should name it by
  its ID;
- the write guard (the fence) checks only the version number of the
  namespace, and should also check that this installation owns the
  namespace;
- the version number should end in two digits that name the installation
  that owns that version, with a signature and the ID of the signing key,
  so that two installations never
  make the same number and no one can claim a number without proof.

ELI5: today the lock on each secret box has the namespace's name on it. If
the name ever changes, no key opens the box. Use the namespace's number,
which never changes. The guard at the door checks the date on your pass,
but not which house it is for: make it check both. And each pass gets the
stamp of the house that issued it.

- Option 1: yes, as one slice of work before the first tag.
- Option 2: no. Accept that a namespace never changes its name, never meets
  a name conflict on another installation, and that the guard checks only
  the version number. Each later change then needs a data conversion.
- **Recommendation: option 1.** Before the first tag each change is an edit
  of the schema and of the guard. After the tag, each one rewrites stored
  data.

### B-3. How is the old primary stopped at a forced failover?

A forced failover makes a copy the new owner of a namespace while the old
owner (the old primary installation) does not answer. If the old primary is
still running and clients still reach it, it keeps accepting their changes,
and those changes are lost. The server cannot check this itself.

ELI5: before you give the spare key to the new house sitter, make sure the
old one cannot get in any more.

- Option 1: before a forced failover, the operator states that the old
  primary is stopped, or cut off from clients in DNS or the load balancer,
  and the server records the statement. The operator also moves the old
  primary to a "fenced peers" list: the other installations refuse its
  forwards and its tokens, and let it read only who the new owner is, so
  that it stops its own writes when it comes back.
- Option 2: no step. The old primary learns of the new owner from the other
  installations when it comes back, and its changes until then are lost.
- Option 3: the server stops the old primary with an API call to it. This
  works only when the old primary answers, and then a planned failover is
  the better tool.
- Option 4, which can be added to option 1: each other installation accepts
  a forced failover only after its own operator confirms it. This stops a
  copy whose signing key was stolen from naming itself the new owner. It
  costs one operator step on each installation, and no write waits for it.
- **Recommendation: option 1, with the check of option 2 as a second
  layer.** Option 4 is a setting that is off by default. The architecture decision of the federation (ADR 248) already
  tells the operator to stop the old primary first. A removal from the peer
  lists alone is not enough, because clients can still reach the old primary.

### B-4. Must the installations of a federation run the same version?

ELI5: two people can only copy each other's notes if they write in the same
language.

- Option 1: yes, until the first tag. An upgrade pauses replication until
  each side runs the new version. After the tag: replicas first, then the
  primary.
- Option 2: allow different versions now, with code that converts each
  row image.
- **Recommendation: option 1.** Before 1.0 there is no conversion code
  (CLAUDE.md).

### C-1. Does a move of a namespace between installations need federation?

ELI5: you can move house with a van (offline: pack, drive, unpack, the
house is closed for the day), or build the new house next door, copy each
change as it happens, and walk across in a minute.

- Option 1: offline move only, from the namespace backup.
- Option 2: online move only, which needs federation.
- Option 3: both. Offline first (slice 2 gives most of it), online after
  replication.
- **Recommendation: option 3.**

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). The
  planned failover pauses the writes of one namespace for the lag. The
  placement read from peers and the restore check read peers, but no write
  waits for them.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
