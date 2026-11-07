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
| GitLab Geo | One primary site, secondary sites. Database by PostgreSQL streaming replication, files by a separate log cursor and verification. Secondaries proxy writes to the primary. Planned failover uses maintenance mode first. | Secondaries forward writes. A planned failover pauses writes first. Verification of what a replica holds. | Two replication mechanisms (database and files). Here one log carries rows and blob references. |
| Vault performance replication | Primary cluster owns writes. Performance secondaries serve reads and forward writes. Local mounts and local tokens never replicate. | A local installation scope that never replicates (principals, sessions, settings of the installation). Tokens of the installation scope work only on their installation (ADR 248 decision 13). | A secondary that holds its own writable data in a replicated namespace. |
| Consul WAN federation and cluster peering, Nomad multi-region | Each datacenter or region has its own consensus and no consensus across them. A request names its target and is forwarded. Peering exports and imports services explicitly, one side at a time. | Forwarding by name. No consensus across installations. A join between namespaces has two halves, one on each side (RFC 79 section 8). | ACL replication from one authoritative region (that is a control home). |
| Temporal global namespaces | One active cluster for each namespace, a failover version, asynchronous replication, and a graceful failover with a `Handover` state that pauses writes until the replica catches up. | All of it. ADR 4 and ADR 248 already follow it. The planned failover below is the Temporal handover. | Failover versions with the cluster index in the low digits (option in "Failover version", below). |
| Shopify pod moves (Ghostferry), Vitess `MoveTables`, Slack workspace migration | Copy the data, then tail the change stream, then a short write freeze and a cutover of the routing row, then a cleanup. | The move of a namespace between installations: a replica, then a planned failover, then a detach. | Dual writes. Here no write ever goes to two places. |
| Fencing tokens and epochs (Martin Kleppmann, "How to do distributed locking"; Chubby sequencers; the PostgreSQL timeline) | Each writer carries an epoch. The storage refuses a write with an old epoch. | The namespace fence and the failover version, which exist (ADR 4). | Nothing new. |

The common answer of these systems: one writer for each partition, a map
from partition to writer, a log from writer to copies, and a cutover that
raises an epoch. OpenDustpan has the partition (the namespace), the writer
(the primary installation) and the epoch (the failover version). It does not
have the log, the forwarding and the cutover. This RFC designs those three.

## What is in place

Facts from `main` at commit `fa62d37`:

- `pkg/namespace`: `Namespace.PrimaryInstallation`, `FailoverVersion`,
  `Fence{Namespace Name, FailoverVersion}`, `ErrNotHome`, `ErrFenced`,
  `WritableHere`. The store checks the fence in the transaction of each
  write.
- Namespace IDs are `ns-` and 20 random lowercase letters and digits
  (`internal/ident.NewID`). All other object IDs use the same function with
  their own prefix (`ws-`, `prn-`, `grt-` and others). So IDs are unique
  across installations with no coordination, and they hold no placement.
- Namespace blobs are under `namespaces/<namespace ID>/`, and
  `pkg/blob/scoped.go` refuses a key outside that prefix.
- A restore raises the failover version of each namespace (ADR 38).
- The inventory keeps a change feed for each namespace:
  `inventory_change_heads` and `inventory_changes (namespace_id, seq)`. It is
  the same shape as the replication log below, for one table.
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
  installation name, a public URL, an internal URL and the identity of its
  TLS client certificate. The static layer only, because an entry decides
  where client tokens go (RFC 37 design point 1, "Security").
- **Placement entries for namespaces that this installation does not hold**:
  an optional static list of `namespace name -> installation name`. A
  front door with no copy (for example the router of OpenDustpan Cloud in
  front of several installations) uses it. OpenDustpan Cloud writes this
  file from its sign-up shim. That is the cell router of AWS and Slack, with
  the map in a file. The same Go code reads it in every build (RFC 37 owner
  constraint 1).
- `GET /api/server/placements/{namespace}` answers the placement with its
  source (`namespace row`, `replica row` or `placement entry`), so a person
  can see why a request went where it went. An edge proxy can cache it.

### A2. The router

A middleware in `pkg/dustpan`, before each tool adapter. Each adapter gives a
resolver from a request to a namespace, as the route table already resolves
the object of each action. The order:

1. The namespace is local and this installation is its primary: serve.
2. The namespace is local as a replica: a read of the console inventory or
   the context API is served from the copy, with the replication lag in the
   answer. Each request of a tool API is forwarded, because a CLI reads what
   it just wrote.
3. A placement entry names another installation: forward.
4. Otherwise: not found, as today.

A route with an ID and no namespace (`/api/v2/workspaces/{id}`, state
versions, uploads, runs):

- A unit never moves between namespaces (ADR 346). So the map from a unit ID
  to its namespace never changes, and a positive cache of it never goes
  stale. Only the map from a namespace to its primary installation changes,
  and the router reads it from the row each time.
- The router finds the ID in the local rows (primary or replica). On a
  miss, it asks each peer with `GET /api/server/placements/by-id/{id}`, in
  parallel, with the timeout of the forward. It caches a hit with no end
  time, and a miss for 10 seconds. The count of peers is small (a few to a
  few tens), so the fan-out is bounded.
- Rejected: a placement hint in the ID. A namespace moves between
  installations (ADR 47 decision 6), so a hint goes stale. Shopify, Slack and
  Vitess keep the shard out of the ID for this reason. See question A-2.

### A3. How a forward works

- `net/http/httputil.ReverseProxy`. Bodies stream and are never buffered. The
  forwarded bytes count against the bound on bodies in flight.
- One hop. The router adds `Dustpan-Forwarded-By: <installation name>`. A
  request that has it is never forwarded again, so two maps that disagree
  give an error, not a loop.
- Mutual TLS between peers. A peer accepts the forward headers only from a
  client certificate in its peer list.
- The token of the caller passes unchanged. The primary authenticates and
  authorizes again (ADR 248 "Not decided", ADR 17). The router never adds
  rights.
- The client address passes in a header that the primary trusts only from a
  peer.
- Absolute URLs (upload URLs, discovery): the router passes
  `Dustpan-Front-Door: <public URL>`. The primary uses it only when it comes
  from a peer and the value is a public URL in its peer list. Otherwise it
  uses its own public URL.
- The primary does not answer (connect error, timeout): 503 with
  `Retry-After` and the name of the primary installation. This is the
  answer of ADR 280 decision 4, which the CLIs already retry.
- Forwarding off: 421 in each adapter, with the name and the public URL of
  the primary installation in the error text. The runner API changes from
  503 to the same rule.
- No redirect. The Go HTTP client drops the `Authorization` header on a
  redirect to another host, so a 307 reaches the primary with no token.

### A4. The console

A console session is installation scope (ADR 248, "A local installation
scope"). It does not forward. A request of the console for a namespace on
another installation gets 421 with the public URL of the primary. The console
then opens that installation, and the person signs in there. With one
identity provider for each federation, that sign-in needs no new password
(RFC 37 design point 3). This is the Vault model: tokens and sessions are
local.

### A5. Lists across installations

`/api/user/stacks` and the inventory list answer the local installation only.
The answer says that it is local, as ADR 17 says for a list that leaves out
objects. A merged list across peers is a later slice (RFC 37 design point 8).

## Part B. Replication and failover (issues #1051, #1052, #1055, #1056, #1058)

### B1. The mechanism: a log of each namespace, in the application

Options:

1. **PostgreSQL logical replication** with row filters. Rejected for the
   stream: it does not work on SQLite, it does not carry blobs, it cannot
   refuse a change with a lower failover version, and 15 tables select the
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

- `replication_log (namespace_id, seq, failover_version, at_ms, kind,
  table_name, row_key, row_image, blob_key)`. `seq` comes from a head row
  for each namespace, as `inventory_change_heads` does. `row_image` is the
  row after the change, in the portable format of ADR 42. A delete is an
  entry with no image. A blob entry names a blob under
  `namespaces/<namespace ID>/`.
- Only the primary appends. The store appends in the transaction that checks
  the fence, so no write can skip the log.
- A blob is written before the row that names it (the rule of today). The
  replica fetches each blob of a batch before it applies the rows of that
  batch, so a replica never holds a row whose blob is missing.
- Blobs never change (ADR 217 decision 18). The log carries the key, never
  the bytes. A shred deletes the item key on each copy, so a shred reaches
  each replica (ADR 151).

The protocol:

- The replica pulls. `GET /replication/v1/namespaces/{id}/changes?after=<seq>`
  over mutual TLS, with a long poll. Pull, not push, because the replica
  owns its cursor, and a primary never waits for a replica (ADR 248 decision
  9).
- The replica applies one batch in one transaction, in order, with its
  cursor in the same transaction. It refuses a batch whose failover version
  is lower than the version it holds.
- **Copy, then stream.** A new replica first restores a namespace backup
  (ADR 218) taken at a known `seq`, then pulls from that `seq`. That is the
  pattern of Ghostferry, Vitess and PostgreSQL logical replication.
- The primary keeps the log until each replica has read it, up to a
  retention limit. A replica that falls behind the limit copies the
  namespace again. The primary reports the lag of each replica, in entries
  and in seconds.

Which tables replicate (issue #1051): each table is one of
`namespace` (has `namespace_id`, replicates) or `installation` (local, never
replicates). One list in code holds the class of each table. A test fails
when a table of the baseline is not on the list. The backup already keeps
such a list (`pkg/backup/portable_tables.go`). This design reuses it.

### B2. Planned failover

The Temporal handover:

1. The operator calls `POST /api/server/namespaces/{id}/failover` with the
   target replica, on the primary.
2. The primary sets the namespace to `handover`. Each write answers 503 with
   `Retry-After`. Each CLI retries.
3. The primary appends one last entry: "primary is now X", with the failover
   version plus one.
4. The replica applies each entry up to that one. Its row then names itself
   as primary, with the new version. It accepts writes.
5. The old primary sees that the replica applied the entry (its cursor) and
   becomes a replica. Forwarding goes to the new primary.

No change is lost. Writes pause for the replication lag at step 3, typically
seconds. A timeout ends the handover with no change when the replica does not
catch up.

### B3. Forced failover and the divergent tail

1. The operator calls the failover route on the replica with
   `force: true` and a reason. The audit entry names the operator and the
   reason.
2. The replica writes its row as primary, with the failover version plus one,
   and records `last_applied_seq` from the old primary.
3. When the old primary returns and reads the new entry, the entries after
   `last_applied_seq` are its divergent tail. It marks the namespace
   `diverged`, refuses writes, and keeps the tail readable for an export
   (ADR 248 decision 11). An operator then makes it a replica again from a
   fresh copy.

Equal versions: a restore on the old primary also raises the failover
version. So the old primary and the new primary can each hold version `n+1`
with different primaries. The apply rule compares the pair
`(failover version, primary installation)`. An equal version with another
primary is a conflict. The replica refuses it and reports both. Option:
the Temporal form, where each installation has a small index and each new
version is the next number that ends in that index, so two installations
never make the same version. Recommendation: the pair rule. It needs no new
setting.

### B4. Keys (issue #1055)

ADR 217: one namespace key wraps the data keys and item keys of the
namespace. Each installation has its own key-encryption key.

- Data keys and item keys replicate as stored, because the namespace key
  wraps them.
- The namespace key cannot replicate as stored, because the key-encryption
  key of the primary wraps it. The replica gives a public transport key when
  it joins as a peer. The primary sends each namespace key version wrapped to
  that key (HPKE, RFC 9180). The replica unwraps it in memory and wraps it
  again with its own key-encryption key. The namespace key record of each
  installation is local, and it holds one wrap.
- A customer key (ADR 217) must be reachable from each installation that
  holds the namespace. The docs name multi-Region KMS keys as the way.

### B5. The discovery document and signing keys (issue #1056)

- Each installation that holds the namespace answers the discovery document
  and the key set from its copy. Only the primary signs.
- The issuer URL never changes (ADR 204 decision 1). So a namespace that may
  ever replicate or move needs an issuer base URL that the operator can
  move to another installation, for example a DNS name of the federation.
  This is a one-way door for each namespace, not for the code. The docs say
  it before the first namespace is made.
- ADR 248 decision 12 decides the keys at a forced failover.

### B6. Joins between namespaces (issue #1058)

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
   lag is small, run the planned failover of B2, then remove the old
   installation as a replica and delete its copy after a retention period.
   This is the Shopify pod move and the Vitess `MoveTables` flow. It loses no
   change, and writes pause for seconds. It needs no code beyond Part B.
2. **Without federation: an offline move.**
   1. A manual lock on the namespace (ADR 213) stops new work.
   2. A namespace backup (ADR 218) to a backup target that both
      installations can read.
   3. A restore into the target installation that keeps the namespace ID and
      the name, sets this installation as primary, and raises the failover
      version. ADR 218 restores only into its own namespace on its own
      installation today. This needs a "restore as a move" mode.
   4. The source keeps a moved marker: the namespace row names the target
      as primary. Each request then gets 421 with the target, or a forward.
   5. The operator changes DNS or the placement entry. The source deletes
      the copy after a retention period.

Both ways keep each grant, profile and setting inside the namespace,
because the namespace moves whole. They break only references to the
installation scope. The move check of a whole namespace (ADR 244 decision 1)
adds these findings:

| Code | Severity | When |
| --- | --- | --- |
| `carry.installation-principal` | warning | a grant names a principal or group of the installation, which does not move |
| `carry.installation-role` | warning | a grant uses a custom role of the installation |
| `carry.forge-connection` | warning | a unit uses a git host connection with the installation scope (ADR 271) |
| `carry.runner-registration` | warning | a runner pool of the namespace moves, but its runners registered with the source installation and must register again with the target |
| `carry.notification-destination` | warning | a rule sends to a notification sink with the installation scope |
| `carry.issuer-url` | error | the issuer base URL of the namespace does not resolve to the target (B5) |

### C4. A unit between installations

The same as between namespaces: export and import, with the move check. No
new mechanism.

## One-way doors before the first tag

Before the first tag, the baseline can change in place (ADR 322). After it,
each item below needs a migration with a data conversion, or cannot change.

| # | Door | State on `main` | Proposal |
| --- | --- | --- | --- |
| 1 | The associated data of each sealed secret | Binds to the namespace **name** (`pkg/secret/secret.go`, `Scope.aad`). | Bind to the namespace **ID**. A ciphertext that shipped with the name can never be read under another name without a rewrap of each value. The issuer path already uses the ID (ADR 204). |
| 2 | Namespace references in rows | 15 tables hold the namespace name, not the ID: `audit_entries`, `audit_sink_cursors`, `collection_members`, `collections`, `grants`, `inventory`, `inventory_deleted`, `inventory_labels`, `manual_locks`, `placement_rules`, `principals`, `secret_keys`, `stacks`, `trust_rules`, `workspaces`. | Key each one by `namespace_id`. A replica that must refuse a name conflict, or a future rename, then touches one row. |
| 3 | The fence | `Fence{Namespace Name, FailoverVersion}`. | Carry the namespace ID. The rule does not change. |
| 4 | IDs | Random, 20 characters, no placement. | Keep. Never add an installation or a region to an ID. A test checks that `ident.NewID` holds no setting. |
| 5 | The class of each table | Implicit. | One list: `namespace` or `installation`. No table holds rows of both. A namespace row may name a row of the installation scope only by its stable ID, and must work when that ID is missing on a replica. |
| 6 | Blob keys | Under `namespaces/<namespace ID>/`, with `blob.Scoped`. | Keep. A conformance test checks that each namespace blob uses the scoped view. |
| 7 | The failover version | `int64`, raised by each restore. | Keep it monotonic and never reset. The apply rule compares `(failover version, primary installation)` (B3). |
| 8 | Order between installations | No code depends on it today. | Order across installations is `(failover version, seq)`, never a wall clock. Timestamps stay data. |
| 9 | The issuer base URL | Per installation. | Docs: a namespace that may move needs a movable host name before it issues its first token (B5). |

Items 1 to 3 are the real doors. They are cheap now: an edit of the
baseline in each dialect, a `make generate`, and a `dev reset`.

## Slices

Each slice lands with its e2e step first (CLAUDE.md, "A shared core, one
adapter per tool"). Slices 0 and 1 can land before the first tag. The others
are "Later" (ADR 280).

| Slice | Scope | e2e step |
| --- | --- | --- |
| 0 | One-way doors 1 to 6: the ID in the secret scope, the fence and the rows; the table class list and its test. | The suites of today pass. A restore of a backup made before the change is refused with `ErrSchemaDiverged` (ADR 322). |
| 1 | One answer for `ErrNotHome`: 421 in each adapter, with the primary and its public URL. | Two installations on one database copy: a write to the replica gives 421 from `pulumi up`, `tofu apply` (`cloud` block) and the http backend, with the primary in the text. |
| 2 | The namespace backup (ADR 218), with the `seq` of the log in the manifest. | Back up one namespace, restore it into a fresh installation, and `pulumi stack export` and `tofu state pull` give the same bytes. |
| 3 | The replication log and the pull protocol; replicas read only. | Primary and replica: `pulumi up` on the primary, then `pulumi stack export` against the replica gives the same deployment after the lag. The same for `tofu apply` and `tofu state pull`. A write at the replica gives 421. |
| 4 | Keys over the stream (B4). | A secret config value set on the primary decrypts on the replica with `pulumi config get --show-secrets` after a planned failover. |
| 5 | Planned failover (B2). | `pulumi up` runs in a loop during the failover. Each update succeeds after retries, and no checkpoint is lost. The same with `tofu apply`. |
| 6 | Forced failover and the divergent tail (B3). | Cut the link, write on both sides, force the failover, restore the link. The old primary refuses writes, and its tail exports. |
| 7 | Request forwarding (Part A). | Each CLI points at the replica. `pulumi up`, `tofu apply` and the http backend succeed, through the forward, with uploads. A stop of the primary gives 503 with `Retry-After`, and the CLI retries. |
| 8 | The move of a namespace: online (C3 way 1) and offline (C3 way 2), and the new carry-over findings. | Move a namespace with both CLIs in a loop: no failed run after retries, and the move check shows each `carry.installation-*` finding. |
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

- Option 1: no hint. IDs stay random. A miss asks the peers, and the answer is
  cached for ever, because a unit never changes namespace.
- Option 2: an installation code in each new ID.
- **Recommendation: option 1.** A namespace moves, so the hint goes stale.

### B-1. Is replication a log in the application?

ELI5: the primary keeps a diary of each change, and the copies read the diary.
The other way is to buy a special database that copies for you, but it makes
each write wait for a vote.

- Option 1: a log in the application, the same on SQLite and PostgreSQL.
- Option 2: PostgreSQL logical replication, PostgreSQL only, blobs apart.
- **Recommendation: option 1.**

### B-2. Do the namespace ID changes (one-way doors 1 to 3) land before the first tag?

ELI5: today the lock on each secret box has the namespace's name on it. If
the name ever changes, no key opens the box. Use the namespace's number,
which never changes.

- Option 1: yes, as slice 0, before the first tag.
- Option 2: no. Accept that a namespace never changes its name and never
  meets a name conflict on a replica.
- **Recommendation: option 1.** It costs one edit of each baseline now.

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
  planned failover pauses the writes of one namespace for the lag. It never
  makes a write wait for a peer.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
