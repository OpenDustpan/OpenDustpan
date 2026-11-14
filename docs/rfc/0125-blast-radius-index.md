---
rfc: 125
title: An index of the unit graph and the blast radius that is fast and small
status: discussing
created: 2026-11-07
---

# RFC 125. An index of the unit graph and the blast radius that is fast and small

Status: discussing. Design only. No code changes with this RFC. Until the
owner decides, ADRs 153, 162, 163, 166, 194 and 226 stay as they are. The
section "ADR changes" lists each decision that this RFC would change.

## Problem

The owner said on 7 November 2026: "Blast radius -> I'm optimistic/this seems
solvable to me, it just needs some thought on how we can effectively index the
information in a performant manner without incurring a large amount of storage
cost."

The accepted design has a graph, but the graph sees few edges:

- ADR 162 walks the dependency edges of ADR 153. ADR 153 records an edge only
  when a unit reads the outputs of another unit through the server, with a job
  token or a lock. A team that runs its tools on workstations gets
  principal-only edges, and those are not graph edges (ADR 162,
  "Consequences").
- ADR 162 decision 3 says that the server cannot map an output to the
  resource that makes it. So a unit that uses a VPC of another unit by its ID,
  with no output read, is not in the graph.
- ADR 163 indexes one resource key (the ARN, the Azure ID or the Google Cloud
  full name) of each managed resource, to find resources that two units
  manage. It does not index the values that a unit **uses**.
- ADR 166 decision 16 keeps the access filter out of the edge store: the
  graph reads the edges and then applies `graph.Access`. The PM asked for a
  design where the store never returns a unit that the caller cannot read.
- No design answers "the blast radius of this plan": which units a planned
  delete or replace reaches.

The cost to avoid: a naive index stores each identity value as text, once for
each unit that holds it. That costs about 160 bytes for each value in SQLite
and about 390 bytes in PostgreSQL (measured below). An index that keeps every
version of each state would multiply that by the count of versions.

The risk to avoid: the state of a unit is input that its writer controls. A
writer can put any reference or any value into a state with
`pulumi stack import` or `tofu state push`. So an edge that comes from state
must not let a writer learn what exists outside their access, block the work
of another team, or push real edges out of an answer.

## What this RFC answers

1. The edges that exist, where each comes from, how each carries its
   confidence and its evidence, and what each edge may do (show, walk, warn,
   block).
2. The index: what it stores, when it changes, how it stays small, its cost
   per 1,000 units and per 1,000,000 resources, and the query plans in both
   SQL dialects.
3. Prior art, with sources.
4. Authorization: the store filters by the read scope of the caller, with
   live data, and no answer is an oracle for what the caller cannot read.
5. The ADR changes, the one-way doors, slices with e2e steps, and the
   questions for the owner.

## 1. The edges

An edge goes from a **source** unit to a **dependent** unit: a change of the
source can break the dependent. Edges between units of two tools are normal
(a Pulumi stack can depend on an OpenTofu workspace, ADR 13).

### The kinds of edge

| Edge | Where it comes from | Exact or inferred | Stored today |
| --- | --- | --- | --- |
| Output read, evidence `job` | A job of the dependent reads the outputs or the state of the source (ADR 153) | Exact | Port and memory adapter, no SQL |
| Output read, evidence `lock` | A principal that holds the lock of another unit reads (ADR 153) | Inferred | Same |
| Declared, evidence `declared` | A `follows` trigger (ADR 267 decision 11), later a person (ADR 313: never expires) | Exact | Triggers table |
| **State reference** (new), evidence `state` | The latest state of the dependent names the source: a Pulumi `StackReference`, an OpenTofu `terraform_remote_state` or `tfe_outputs` data source | Exact when verified, else inferred (see below) | No |
| **Identity match** (new), evidence `identity` | A value in the latest state of the dependent is equal to an identity value of a resource that the source manages | Inferred | No |
| Profile attachment (anchor, not an edge) | A unit attaches a profile (ADR 203) | Exact | Profile attachments |
| Managed twice (link, not an edge) | Two units manage one resource (ADR 163, ADR 165) | Exact | Resource index port |

### State references

The state shapes, from the source code of each tool:

- **Pulumi.** The SDK registers a stack reference as a read resource of type
  `pulumi:pulumi:StackReference`. Its only input is `name`, and its `id` is the
  name of the referenced stack. Its outputs are `name`, `outputs` and
  `secretOutputNames`.
  ([builtins.go](https://raw.githubusercontent.com/pulumi/pulumi/master/pkg/resource/deploy/builtins.go),
  [stackReference.ts](https://raw.githubusercontent.com/pulumi/pulumi/master/sdk/nodejs/stackReference.ts))
- **OpenTofu, `terraform_remote_state`.** A resource with `mode: "data"` and
  type `terraform_remote_state`, with the attributes `backend`, `config`,
  `workspace`, `defaults` and the computed `outputs`.
  ([data_source_state.go](https://raw.githubusercontent.com/opentofu/opentofu/main/internal/builtin/providers/tf/data_source_state.go))
- **OpenTofu, `tfe_outputs`.** A data resource of type `tfe_outputs` with
  `organization` and `workspace`.
  ([tfe_outputs docs](https://raw.githubusercontent.com/hashicorp/terraform-provider-tfe/main/website/docs/d/outputs.html.markdown))

The projection run (ADR 166 decision 7) reads these from the masked state
and resolves each name to a unit of this installation:

- Pulumi: `org/project/stack`, with the namespace from `org`.
- OpenTofu: `backend` is `remote` or `cloud`, and `config.hostname` is the
  public host of this installation, or `tfe_outputs` with a host of this
  installation. The namespace is `organization`, the workspace is
  `workspaces.name` or `workspace`.

A writer can plant a reference: a state push or an import can name any unit,
with no read of it. So the run applies these rules, in order:

1. **No resolve across namespaces without consent.** A reference to a unit of
   another namespace resolves only when a live namespace link joins the two
   namespaces (ADR 163 decision 9, ADR 216), or a live sharing rule of the
   producer covers the dependent (ADR 273). Else the reference makes no edge,
   and the run does not look up whether the name exists.
2. **Verified or not.** A resolved reference is **verified** when the
   principal that wrote the state item holds the read of the outputs of the
   source (`unit:read-outputs`, ADR 273) when the run checks it. The run
   checks it at the point of use, with the authorizer, and records the
   result on the edge. A verified edge has the confidence `exact`. An edge
   that is not verified has the confidence `weak`. Nothing else about it
   changes: it shows and it is walked, with its mark.
3. **No count by name.** The run stores no count of references that did not
   resolve. Each answer gives, for a unit whose state the caller can read,
   one number: **references not shown**. It counts the references that did
   not resolve, that resolve to a unit that the caller cannot read, or that
   rule 1 refused. One number for all three cases means that a writer cannot
   tell "no such unit" from "a unit that you cannot read". The answer holds
   the number only when the caller has the read of the state of that unit,
   because the caller can then count the references in the state anyway.

### Identity matches

A unit **produces** an identity value when it manages a resource with that
identity. A unit **consumes** a value when its state holds the value in the
attributes of a resource, a data source or a read resource. A source produces
a value and a dependent consumes it: that is an identity match.

- **Produced values** extend the key rules of ADR 163 decision 3 with the
  resource ID: the ARN and the `id` for AWS, the resource ID for Azure, the
  full resource name and the self link for Google Cloud. A data source or a
  read resource never produces a value (ADR 163 decision 4).
- **Consumed values** are the string attributes of each resource and data
  source that have an **identity shape** (an allowlist, see "Extraction
  rules"). Pulumi gives the inputs of a resource. OpenTofu gives the
  attributes. A string that holds JSON, for example an IAM policy document,
  is scanned for ARNs.
- A value that the unit also produces is internal to the unit and is dropped.
- A consumed value with no producer in the scope makes no edge. This is the
  noise filter: an account ID, a region or an AMI of a vendor never makes an
  edge, because no unit manages it.
- A match across namespaces needs a live namespace link (ADR 163 decision
  9). A sharing rule is not enough, because it shares outputs, not the
  resources of the producer.

A shared VPC, subnet or security group needs no special case. The unit that
manages the VPC produces its ID, and each unit that puts a subnet in it
consumes the ID. Two units that consume the same VPC do not get an edge
between them: they are siblings, not dependents. A VPC that no unit manages
makes no edge. The console can show the units that use one value (the
**value view**, slice 7), but a walk does not follow it.

DNS names (a load balancer name in the configuration of another unit) are a
later slice, with the confidence `hint`. A DNS name can point to many things,
and a name that a unit uses can be a CNAME of a name that another unit makes.

### Confidence, and what an edge may do ("No magic")

Confidence is not a stored score. It is a function of the evidence, in one
table in the catalog of code rules. The API gives the evidence, the
confidence and the rule that gave the confidence. What an edge may do is a
second function of the same evidence, in the same table:

| Evidence | Confidence | Shown | Walked by default | Gate fact `dependents` | Blocks a delete |
| --- | --- | --- | --- | --- | --- |
| `job` | `exact` | yes | yes | yes | yes (ADR 153 decision 5) |
| `declared` | `exact` | yes | yes | yes | yes (ADR 153 decision 5) |
| `lock`, not ambiguous | `weak` | yes | yes | yes | yes, today (ADR 153 decision 5, owner question 3) |
| `state`, verified | `exact` | yes | yes | yes | yes (owner question 3) |
| `state`, not verified | `weak` | yes | yes | no | no |
| `identity` on an ARN, an Azure resource ID or a Google Cloud full name | `strong` | yes | yes (owner question 1) | no | no |
| `identity` on a short ID (`subnet-…`, `sg-…`, `vpc-…`) | `weak` | yes | yes (owner question 1) | no | no |
| `lock`, ambiguous, and DNS (later) | `hint` | yes | no | no | no |

The rules behind the table:

- **An inferred edge never blocks or denies alone.** Only the evidence that
  ADR 153 decision 5 lets block, plus a verified state reference, enters the
  gate fact `dependents` (ADR 226) and the delete check. So a planted value
  or a planted reference can never make a gate deny, require an approval, or
  stop a delete. An inferred edge shows in the delete check and in the
  review of a change order (ADR 194 decision 7) as a warning, with its
  confidence mark.
- **A verified state reference blocks only for a writer who could read the
  source.** Rule 2 above makes the edge `exact` only when the writer of the
  dependent holds `unit:read-outputs` on the source. A writer with no read
  of a unit cannot block its delete.
- **The order of a cut is by confidence first.** When a hop reaches its
  limit of 5,000 edges (ADR 162 decision 9), the store orders by confidence
  (`exact`, `strong`, `weak`, `hint`), then by the last time seen. So
  planted inferred edges never push an exact edge out of an answer.

Each edge in an answer holds `evidence`, `confidence`, `verified` (for
`state`), `firstSeen`, `lastSeen` and, for `identity`, `matches` (the count of
distinct values). The edge row stores no value from the state.

A route `GET .../graph/edges/{source}/{dependent}/evidence` reads the latest
masked state of each unit and lists up to 50 value pairs of one edge: the
value, the resource key in each unit and the attribute path in the dependent,
for example `vpc-0e9801d129example: module.vpc.aws_vpc.main → aws_subnet.a
.vpc_id`. It needs the read of the state of both units. It compares the raw
values. A hash match with no raw match is a **collision**: the route shows
it as one, and the server writes an automatic dismissal with the reason
`hash collision` and the actor `system` (see below), so the walk drops the
edge and a person can see why.

A person can **dismiss** an inferred edge with a reason. A dismissal is a
stored record (holder, edge, reason, person or `system`, time), listed and
revocable, in the backup. A walk does not follow a dismissed edge, and the
answer counts it in `dismissed`. A person can also declare an edge (ADR 313).
Owner question 4 asks who can dismiss.

## 2. The index

### What to store

Six projection tables and one data table. The projection tables are in
`backup.ProjectionTables` (ADR 166 decision 5): a backup leaves them out, and
a restore rebuilds them from the latest state items. So their layout is a
two-way door: a change is a rebuild, not a migration of data.

| Table | Row | Key | Class |
| --- | --- | --- | --- |
| `graph_unit_refs` | one for each unit: a local integer `unit_seq` and `ns_seq` for (namespace ID, tool, unit ID) | `unit_seq`; unique (namespace ID, tool, unit ID) | projection |
| `graph_namespace_refs` | one for each namespace: `ns_seq` for the namespace ID | `ns_seq`; unique namespace ID | projection |
| `identity_produced` | one for each distinct value that a unit produces, with its class (strong or weak) | (`ns_seq`, `value_hash`, `unit_seq`) | projection |
| `identity_consumed` | one for each distinct value that a unit consumes | (`ns_seq`, `value_hash`, `unit_seq`) | projection |
| `identity_set_chunks` | the value sets of each unit, split by hash prefix into chunks of at most 240 values (1,920 bytes) | (`unit_seq`, `side`, `bucket`) | projection |
| `graph_derived_edges` | one for each (source, dependent, evidence) of the kinds `state` and `identity`, with `rank`, `matches`, `verified`, the deleted mark, the generation of each end, and the times | (`source_seq`, `dependent_seq`, `evidence`); index (`dependent_seq`, `source_seq`) | projection |
| `graph_edge_dismissals` | one for each dismissal, by stable IDs | (namespace ID, source, dependent, evidence) | data, backed up |

`graph_unit_refs` holds no authorization input. The collection of a unit,
the namespace links and the sharing rules are read from their live tables in
each query (section 4).

The reverse index is `identity_produced` and `identity_consumed`: from a value
to its units. The key leads with the namespace, so each lookup stays in one
namespace (and its linked namespaces), and a purge of a namespace is one
range delete. In SQLite, these tables and `graph_derived_edges` are
`WITHOUT ROWID` and `STRICT` (measured below: 2.8 times smaller than a rowid
table with a primary key index, and stable under churn). No table of the
SQLite baseline is `WITHOUT ROWID` today, so this is a new pattern for the
baseline (owner question 6).

The edges of ADR 153 stay in their own table: they are recorded facts, not a
projection, and a backup holds them. A hop reads both tables (the graph
adapter makes one `UNION ALL`).

### How to keep it small

Each choice below was measured (see "Measurements").

1. **Hash the values.** `value_hash` is the first 8 bytes of HMAC-SHA-256 of
   the normalized value. See "The hash key" for the key and the threat model.
2. **Store only identity-shaped values.** The allowlist keeps 2 produced
   values for each resource and between about 1 and 5 consumed values (the
   two synthetic shapes below), not the 20 to 100 attributes of a resource.
3. **Dedupe for each unit.** A unit with 300 subnets that all use one VPC ID
   consumes the value one time. Edges are between units, so the resource that
   holds a value is not in the row. The evidence route finds it in the state.
4. **Intern the long keys.** A unit reference is a namespace ID, a tool and a
   unit ID, about 60 bytes of text. Rows hold an 8-byte `unit_seq` and a
   4-byte `ns_seq`. The integers are local to one installation. This is safe
   because the tables are a projection: a restore, a replica installation
   (ADR 248) or a new hash key rebuilds them.
5. **Chunked per-unit sets for the diff.** The run must know the old values
   of a unit to write only what changed (ADR 163 decision 5). A second index
   on (`unit_seq`, `value_hash`) costs about 20 bytes in SQLite and 36 bytes
   in PostgreSQL for each row. A whole sorted array for each unit costs 8
   bytes for each value, but a change rewrites all of it: measured, a
   3-value change of a unit with 60,000 values wrote 1.08 MB of WAL. Chunks
   by hash prefix, at most 240 values each so that no chunk goes to TOAST,
   cost about 9 bytes for each value, and the same change rewrites only the
   chunks that hold a changed value: measured with 128 chunks, 108 KB of WAL.
6. **Only the latest state.** The index holds the latest state item of each
   unit, never the history. A point-in-time graph reads history (ADR 150), not
   this index.
7. **Partition by namespace through the key**, not by a partitioned table.
   Every row leads with `ns_seq`. Both dialects then read one namespace as one
   key range, and a purge deletes one range.
8. **Compaction on each state version** is the diff write: the run deletes
   the values that the new item no longer holds, inserts the new ones, and
   recomputes the edges of the unit. No row of an old item stays.

### The hash key

- **Derivation.** HKDF-SHA-256 (in the Go standard library) from the
  built-in key-encryption key of the installation (ADR 23), with its own
  label, `opendustpan graph value hash v1`. The key is never stored in the
  database. It does not change when the per-scope secret keys rotate. A
  change of the key-encryption key, or of the label, changes the hash key:
  the server then rebuilds the whole index, and every unit shows in
  `indexBehind` until its run ends.
- **The hash never leaves the server.** No API, export, metric or log holds
  it.
- **False edges.** A false edge needs a produced hash to equal a consumed
  hash in one scope. With 2 million produced and 1.5 million consumed values
  in one scope, the chance is about 2 × 10^6 × 1.5 × 10^6 / 2^64 ≈ 1.6 ×
  10^-7. The evidence route finds such an edge (see above).
- **Threat model.** Without the key, a person who gets the index (a copy of
  the database) cannot test a guessed value, so a dictionary attack does not
  work. With the key, the values are easy to recover: an ARN has a known
  form, account IDs are known, names are often guessable, and an old short
  ID has only 8 hex characters (32 bits). So the key gets the protection of
  the key-encryption key, and the index is not a place to hide values. The
  values are IDs, not secrets: a secret value never reaches the index (the
  run reads the masked view, ADR 41).

### Extraction rules (security code)

The state is untrusted input (ADR 250). The rules:

1. **Allowlist at the boundary.** A consumed value is kept only when it
   matches one shape, else it is dropped:
   - ARN: `arn:<partition>:<service>:<region>?:<account>?:<resource>`, with a
     known partition, at most 2,048 bytes.
   - AWS short ID: a prefix from a fixed list (`vpc`, `subnet`, `sg`, `igw`,
     `nat`, `rtb`, `eni`, `i`, `vol`, `tgw`, `vpce`, `pcx`, ...) and 8 or 17
     lowercase hex characters
     ([EC2 ID formats](https://aws.amazon.com/blogs/compute/longer-resource-ids-in-2018-for-amazon-ec2-amazon-ebs-and-amazon-vpc)).
   - Azure: `/subscriptions/<GUID>/resourceGroups/...`, normalized to
     lowercase with no trailing slash (ADR 163 decision 2).
   - Google Cloud: `projects/...` full names, and self links on
     `www.googleapis.com` or `compute.googleapis.com`, normalized to the
     `projects/...` form.
2. **Contain.** The value goes into the HMAC and the hash goes into a
   placeholder. No value reaches SQL text, a log line or an error.
3. **Limit.** The limits bound the work of one run and the flood that one
   writer can make:

   | Limit | Value |
   | --- | --- |
   | Consumed values for each resource | 64 |
   | Distinct consumed values for each unit | 20,000 |
   | Distinct produced values for each unit | 200,000 (2 for each of the 100,000 resources of ADR 163 decision 7) |
   | Identity edges upstream of one unit (as the dependent) | 500 |
   | New identity edges of one unit in one run | 100 |
   | JSON scan of one string | 64 KiB, depth 8 |
   | Time of one run | 60 seconds, then the run ends with `complete: false` |

   A unit above a limit keeps the values or edges with the lowest hashes (so
   a later run keeps the same ones) and gets the mark **not fully indexed**
   (ADR 163 decision 7). The limit of 500 upstream edges stops one unit from
   making an edge to each producer of a namespace. The limit of 100 new edges
   in one run stops a flood from changing many answers at once. Units of the
   synthetic data set stay far below both: each has at most 6 upstream
   edges.
4. **Authorize at the point of use.** See section 4, and rule 2 of the state
   references.
5. **Fail closed, and say so.** A state that does not parse makes no new
   values and no new edges. The run keeps nothing of the older item as if it
   were current: it deletes the derived edges of the unit, marks the unit
   **not fully indexed** with the reason, and the record of the run holds the
   error. Section "Completeness" says how an answer shows it.

Each step gets its own test with hostile input, as ADR 250 asks: a 2-MiB ARN,
a JSON bomb in a policy, 10^6 values in one resource, 10^5 guessed ARNs in
one unit (the flood test: at most 500 edges, the exact edges of the answer
unchanged), a short ID with upper case, a state that does not parse (the
answer says `complete: false`), a planted reference to a unit of another
namespace with no link (no edge, and the same "references not shown" as a
name that does not exist), and a caller scope that covers nothing.

### When the index changes

**As a durable follow-up, not in the transaction of the state write.** A
Pulumi checkpoint or an OpenTofu state version is on the hot path of the CLI.
A parse of a 100-MB state in that transaction would hold it for seconds. The
index changes in the projection run of ADR 166 decision 7, which already
reads each latest state item one time, masked, coalesced for each unit. The
steps of that run, extended:

1. Read the latest masked item (as today).
2. Context and resource index (as today).
3. **Values, in short transactions.** Extract the produced and consumed sets.
   For each chunk of `identity_set_chunks` whose values changed, in one short
   transaction with the fence of the namespace: write the chunk, delete the
   removed values and insert the added values of that chunk. Then increment
   the **generation** of the unit (a counter in `graph_unit_refs`). Each
   transaction writes at most 1,000 rows.
4. **Edges, with no lock.** Read the edges where the unit is the source or
   the dependent from the committed values: look up the produced hashes of
   the unit in `identity_consumed` and its consumed hashes in
   `identity_produced`, in batches of 1,000, in read transactions. Resolve
   the state references. Then write the difference to `graph_derived_edges`
   in short transactions of at most 1,000 rows, each an upsert with a
   generation check:

   ```sql
   INSERT INTO graph_derived_edges (source_seq, dependent_seq, evidence, rank,
     matches, verified, gen_source, gen_dependent, first_seen_ms, last_seen_ms)
   VALUES (...)
   ON CONFLICT (source_seq, dependent_seq, evidence) DO UPDATE
     SET rank = excluded.rank, matches = excluded.matches,
         verified = excluded.verified, gen_source = excluded.gen_source,
         gen_dependent = excluded.gen_dependent,
         last_seen_ms = excluded.last_seen_ms
     WHERE excluded.gen_source >= graph_derived_edges.gen_source
       AND excluded.gen_dependent >= graph_derived_edges.gen_dependent;
   ```

   A removed edge is an upsert with `matches = 0` (a tombstone), so a later
   write with older generations cannot bring it back. The next run of either
   unit deletes tombstones with both generations older than its own.

   **Why this is correct with no lock.** Each run commits its values (step 3)
   before it reads the values of other units (step 4). For two runs of units
   A and B that touch one edge, the run that reads second sees the committed
   values of both, so it computes the edge from the newest generations of
   both ends, and the check lets its write win over a write of the other
   run, in whatever order they commit. Both dialects support this form of
   upsert.
5. **Across linked namespaces.** The edge row lives in the namespace of the
   dependent, and its write carries the fence of that namespace, so no run
   writes into the half of another namespace (ADR 248). When a run of a
   source finds consumers in a linked namespace, it starts a durable
   operation `graph.relink:<namespace ID>` for that namespace, with one start
   key for each namespace, so the operations coalesce like the projection
   run. That operation recomputes the upstream edges of the found units under
   the fence of their namespace. An unlink starts `graph.relink` for both
   namespaces in the transaction of the unlink, and the read checks the link
   at query time anyway (section 4).

**The SQLite writer.** SQLite has one write connection, and ADR 274 decision
3 gives it a wait limit that fails closed. The CLI writes a checkpoint or a
state version on the same connection. So no projection transaction may hold
the writer long: each transaction above writes at most 1,000 rows, and
measured, the largest one took 3.9 ms (a hub unit, below). A projection
transaction that waits past half of the wait limit gives up and retries
later, so the CLI writes never wait behind it.

**Metrics.** The server exposes the projection lag (the age of the oldest
state item whose run has not ended), the time of each edge transaction and
the wait for the SQLite writer. An operator sees a slow index before a user
does.

A derived edge has no window (ADR 162 decision 8): it exists while the two
latest states match. `firstSeen` and `lastSeen` are the times of the runs
that first and last confirmed it. The record of each run holds the counts of
edges added and removed. A soft delete keeps the derived edges with the
deleted mark, so the blast radius of a restore is known. A purge drops them.

### Completeness

ADR 162 decision 12 gives each answer `complete`, `truncated`, `hidden` and
`since`. This RFC adds:

- `notFullyIndexed`: the count of units in the answer, or one hop beyond a
  unit in the answer, that are marked not fully indexed (a limit, a parse
  failure or a failed run).
- `indexBehind`: the count of units in the answer whose run is behind their
  latest state item (ADR 166, `ErrIndexBehind`).
- `dismissed`: the count of edges that the walk did not follow because a
  person or the system dismissed them.
- `inferredScope`: always the text `readable-units-only` (see section 4).

`complete` is false when `truncated`, `notFullyIndexed` or `indexBehind` is
not zero. For a blast radius, fewer edges is the unsafe direction, so an
answer never reads as complete when an index gap could hide a unit.

### Measurements

**Where the numbers come from.** The scripts that made these numbers are in
[`bench/graphindex/`](../../bench/graphindex/), with a README that gives the
steps. `make check` and `make bench` do not run them. They ran one time, on
the hardware below, and the raw results are not in this repository. Slice 1
moves the benchmark into `make bench`, and its results replace these numbers.
Until then, treat each number here as one measurement that nobody repeated.

**The data set.** One synthetic data set for both dialects, made by one
script with a fixed seed: 3,040 units and 1,064,500 resources in 10
namespaces. Unit sizes are skewed: 10 units of 30,000 resources (one in each
namespace), 40 units of 2,000, and the rest from 50 to 400. Namespace 0 holds
2,100 units, so its large unit is a hub with 2,103 consumers. Each resource
produces 2 values (2,129,000 rows). Each unit that is not one of the large
units consumes 1.5 values for each of its resources, 60% from the hub of its
namespace and 40% from 5 neighbor units, with duplicates removed (1,146,007
rows). This gives 18,180 identity edges, 6 upstream edges for each consumer
unit.

**The hardware.** A container with 4 vCPUs (Intel Xeon at 2.1 GHz) and 16 GB
of memory, a local virtio disk, not network storage. PostgreSQL 18.6 with
`shared_buffers` of 512 MB. SQLite 3.53.1 in WAL mode. "Cold" means after a
restart of PostgreSQL and a drop of the OS page cache before each query.
Network storage would make cold reads slower. This RFC did not measure it.

**Size, fresh and after churn.** Churn is 5 cycles in which 10% of the values
of each table change (the same deletes and inserts in each dialect), then a
`VACUUM` in PostgreSQL.

| Table | PostgreSQL fresh | PostgreSQL after churn | SQLite `WITHOUT ROWID` fresh | after churn | SQLite rowid + primary key index (baseline form) fresh | after churn |
| --- | --- | --- | --- | --- | --- | --- |
| Produced values (bytes per row) | 113 | 152 | 20.7 | 21.0 | 45.5 | 59.9 |
| Consumed values (bytes per row) | 106 | 141 | 19.1 | 19.5 | 44.0 | 57.0 |
| Derived edges (bytes per row) | 177 | not churned | 47.5 | not churned | 63.3 | not churned |
| Chunked per-unit sets (bytes per value) | ~9 | not churned | ~9 | not churned | ~9 | not churned |

- PostgreSQL grows by a factor of 1.34 and stops growing after cycle 3.
  `VACUUM` gives no space back: random hashes split B-tree pages, and a
  B-tree does not merge them.
- SQLite `WITHOUT ROWID` grows by 1.5%. Its pages were already at a steady
  fill, because the load was in hash order, which is random.
- The SQLite rowid form grows by about 5% in each cycle and had not stopped
  after 5 cycles. With no `auto_vacuum` in the baseline, the file never
  shrinks. This is the reason for `WITHOUT ROWID`.
- Text values in place of hashes (measured on an earlier data set of
  2,000,400 rows): 390 bytes for each row in PostgreSQL and 160 in SQLite.

**Storage estimate.** The consumed values for each resource depend on the
resource types. The data set above has 1.08 for each resource (the large
units consume nothing). A synthetic OpenTofu state of `aws_instance`
resources (an AMI, a subnet, a security group, a volume, a network
interface, a KMS key and an IAM policy with two ARNs) keeps 7 distinct values
for each resource: 2 produced and 5 consumed. With the sizes after churn:

| Size | Consumed for each resource | PostgreSQL | SQLite `WITHOUT ROWID` | SQLite rowid form |
| --- | --- | --- | --- | --- |
| Per resource | 1.1 | ~490 B | ~90 B | ~210 B |
| Per resource | 5 | ~1,070 B | ~200 B | ~470 B |
| 1,000 units of 300 resources | 1.1 to 5 | ~145 to 320 MB | ~27 to 60 MB | ~63 to 140 MB |
| 1,000,000 resources | 1.1 to 5 | ~0.49 to 1.07 GB | ~90 to 200 MB | ~210 to 470 MB |

Plus the edges: at 10 edges for each unit, 1,000 units have 10,000 edges,
about 1.8 MB in PostgreSQL.

**Against the state.** Real states hold about 0.9 to 12 KB for each resource,
and most Terraform states hold 4 to 7 KB:

| Source | Tool | Resources | Size | Bytes/resource |
| --- | --- | --- | --- | --- |
| [terraform#16375](https://github.com/hashicorp/terraform/issues/16375) | Terraform | ~1,600 | 8.6 MB | ~5,400 |
| Same issue, a later comment | Terraform | ~32,000 | 380 MB | ~11,900 |
| [terraform#33032](https://github.com/hashicorp/terraform/issues/33032) | Terraform | 10,000 | 40 MB | ~4,000 |
| [terraform#35822](https://github.com/hashicorp/terraform/issues/35822) | Terraform | 14,160 | 100 MB | ~7,060 |
| [opentofu#1647](https://github.com/opentofu/opentofu/pull/1647) | OpenTofu | ~450,000 objects | 386.6 MB compact | ~860 |
| [pulumi#24408](https://github.com/pulumi/pulumi/issues/24408) | Pulumi | ~30,000 | 85 MB | ~2,830 |

So against one copy of the latest state at 4 to 7 KB for each resource, the
index is about 7% to 27% in PostgreSQL, 1.3% to 5% in SQLite with
`WITHOUT ROWID`, and 3% to 12% in the SQLite rowid form. The server keeps
many state versions, so against all stored state the share is smaller. In
PostgreSQL, most of each row is the fixed overhead (a 23-byte tuple header
and a 4-byte item pointer for each heap row, and about 20 bytes for each
B-tree entry,
[page layout](https://www.postgresql.org/docs/current/storage-page-layout.html)).
If PostgreSQL size matters, the consumed side can move to one `int8[]` for
each chunk with a GIN index (measured fresh: 28 bytes for each value, 3.7
times smaller). The cost: the two dialects then have different query types,
and ADR 274 decision 1 asks for the same types in both. So this RFC keeps
rows, and names the array as a later change for PostgreSQL only (owner
question 6).

**Query times.** Same data, same parameters, same scope (6 of 10 namespaces
and one collection). Each array parameter is JSON text in both dialects (see
"Query plans"). PostgreSQL times are from `psql` `\timing`.

| Query | PostgreSQL cold | PostgreSQL warm | SQLite `WITHOUT ROWID` cold | warm | SQLite rowid form cold | warm |
| --- | --- | --- | --- | --- | --- | --- |
| Plan hits: 1,000 hashes of the hub to their consumers (1,922 units) | 89.5 ms | 83.1 ms | 53.3 ms | 47.1 ms | 56.2 ms | 66.6 ms |
| One hop downstream, frontier of 500 units with the hub (4,568 edges) | 31.4 ms | 18.3 ms | 8.6 ms | 6.1 ms | 8.9 ms | 4.8 ms |
| One hop upstream, the same frontier (2,994 edges) | 16.0 ms | 9.0 ms | 14.4 ms | 8.1 ms | 14.0 ms | 9.2 ms |
| Hidden units of the downstream hop | 26.1 ms | 3.9 ms | 7.0 ms | 1.7 ms | 5.9 ms | 2.3 ms |

A walk of 5 hops is then at most 5 statements of the hop lines.

**A hub run.** The hub produces 60,000 values that 2,103 units consume. Its
run makes 60 read probes of 1,000 hashes and 3 edge transactions of at most
1,000 rows. PostgreSQL: each probe executed in about 6.4 ms (from `EXPLAIN
ANALYZE`), about 0.4 s for all 60. Each edge transaction took at most about
19 ms (55 ms measured by a client that starts `psql` each time, minus the
36 ms that a `SELECT 1` takes in the same client). SQLite: 228 ms for the 60
probes, at most 4.8 ms each, and at most 3.9 ms for each edge transaction. So
no transaction of a hub run holds the SQLite writer for more than a few
milliseconds.

**A rebuild.** A restore and a change of the hash key rebuild the index from
every latest state item. The cost is in the extraction, not in SQL:

| Step | Measured | For 1,000,000 resources |
| --- | --- | --- |
| Extraction in Go (JSON decode, allowlist, JSON-string scan, HMAC), synthetic `aws_instance` state of 3.5 KB for each resource | 32 to 54 MB/s on one core, in 5 runs at 20,000 and 100,000 resources | ~5 GB of state at 5 KB for each resource: ~93 to 156 core-seconds, ~25 to 40 s on 4 cores |
| Bulk insert of the produced values | PostgreSQL `COPY` of 2,129,000 rows into the keyed table: 6.1 s. SQLite: 9.6 s for 3.3 million rows from Python | ~10 s |
| All edges from the values | PostgreSQL 0.63 s, SQLite `WITHOUT ROWID` 1.16 s, rowid form 1.89 s | ~1 to 2 s |

The read and the decompression of the state blobs are not measured, and a
rebuild runs as many runs for each unit, not as one bulk load. So a rebuild
of 1,000,000 resources takes minutes, and each unit shows in `indexBehind`
until its run ends.

**Not yet measured.** Real states (slice 1 measures the consumed values for
each resource on the e2e fixtures and on states that an operator gives), a
churn of the edge table, and network storage.

### Query plans

The graph keeps the walk of ADR 162: hop by hop in Go, with a visited set,
one batch of at most 500 units for each hop, at most 5,000 edges for each
hop. Each hop is one statement with the scope filter in it (section 4).

**One parameter type in both dialects.** Each list (the frontier, the hashes,
the namespaces, the collections, the units of the scope) is one JSON text
parameter. PostgreSQL reads it with
`jsonb_array_elements_text($1::jsonb)`, SQLite with `json_each(?1)`. The
shared generated `Querier` of ADR 274 decision 1 then sees `string` in both,
and `go/types.Identical` holds.

**"What depends on X", one hop downstream, PostgreSQL.** `$1` is the
frontier. The scope is `$2` (all), `$3` (namespace IDs), `$4` (collection
IDs) and `$5` (unit IDs), from `ListObjects`:

```sql
SELECT e.source_seq, e.dependent_seq, e.evidence, e.rank, e.matches,
       e.verified, e.last_seen_ms
FROM graph_derived_edges e
JOIN graph_unit_refs s ON s.unit_seq = e.source_seq
JOIN graph_unit_refs d ON d.unit_seq = e.dependent_seq
LEFT JOIN collection_members m ON m.unit_id = d.unit_id      -- live membership
LEFT JOIN collections c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE e.source_seq IN (SELECT jsonb_array_elements_text($1::jsonb)::bigint)
  AND e.matches > 0 AND e.deleted = 0
  AND ($2 OR d.namespace_id IN (SELECT jsonb_array_elements_text($3::jsonb))
          OR c.id            IN (SELECT jsonb_array_elements_text($4::jsonb))
          OR d.unit_id       IN (SELECT jsonb_array_elements_text($5::jsonb)))
  AND (s.ns_seq = d.ns_seq OR EXISTS (SELECT 1 FROM namespace_links l -- live link
        WHERE l.a = least(s.namespace_id, d.namespace_id)
          AND l.b = greatest(s.namespace_id, d.namespace_id)
          AND l.deleted = 0))
  AND NOT EXISTS (SELECT 1 FROM graph_edge_dismissals x
                  WHERE x.namespace_id = d.namespace_id
                    AND x.source_tool = s.tool AND x.source_unit_id = s.unit_id
                    AND x.dependent_tool = d.tool AND x.dependent_unit_id = d.unit_id
                    AND x.evidence = e.evidence)
ORDER BY e.rank, e.last_seen_ms DESC, e.source_seq, e.dependent_seq
LIMIT 5001;
```

The link check applies to `identity` edges. A `state` edge across namespaces
checks the link or the sharing rule of the producer in the same way.
`namespace_links` stands for the SQL table of `unmanaged.Links` (ADR 163),
which has no SQL adapter yet. The plan in the measurement: an index scan on
the primary key `(source_seq, dependent_seq, evidence)`, a hash join to the
units, and a top-N sort. Upstream uses the index `(dependent_seq,
source_seq)`.

**Hidden units.** A second statement in the same read transaction, with the
scope condition negated, returns the **opaque `unit_seq`** of each hidden
unit, never a count of edges. The walk keeps them in its `hidden` set, so a
unit counts one time across hops and across the two layers (today
`pkg/graph` counts distinct hidden units, and this stays). Only exact edges
count: see section 4.

**SQLite.** The same statement, with `IN (SELECT value FROM json_each(?1))`
for each list. The plan is `SEARCH graph_derived_edges USING PRIMARY KEY
(source_seq=?)` for each unit of the frontier, then a temporary B-tree for
the order.

**"The blast radius of this plan".** The input is a stored plan (RFC 28) or
the plan of a job, never a value from the caller:

1. From the plan, the resources with the action delete or replace, and the
   outputs that the plan removes or changes.
2. The server reads the identity values of those resources from the latest
   state of the unit, and hashes them.
3. Direct hits, with the reason for each unit:

   ```sql
   -- PostgreSQL. $1: the namespace and its live linked namespaces.
   -- $2: at most 1,000 hashes for each call. Both are JSON text.
   SELECT c.unit_seq, count(*) AS hits
   FROM identity_consumed c
   JOIN graph_unit_refs d ON d.unit_seq = c.unit_seq
   WHERE c.ns_seq IN (SELECT jsonb_array_elements_text($1::jsonb)::int)
     AND c.value_hash IN (SELECT jsonb_array_elements_text($2::jsonb)::bigint)
     AND (<the scope filter on d, as above>)
   GROUP BY c.unit_seq;
   ```

   SQLite: the same with `json_each`, plan `SEARCH identity_consumed USING
   PRIMARY KEY (ns_seq=? AND value_hash=?)`.

   Plus, when an output changes, the units with an exact edge from the unit
   (output read or verified state reference): the server cannot tell which
   output each reader uses (ADR 153 decision 16).
4. A walk of the remaining hops (default 2 in all) from the direct hits.

The answer lists each unit with its reason (`value`, `output` or
`transitive`), its hop, and the lowest confidence on its path.

**Why not one recursive CTE.** On an earlier, denser data set (PostgreSQL
only, 493,288 edges), a `WITH RECURSIVE` walk of 3 hops from one unit, with a
path array to stop cycles, made 858,212 rows, wrote 69 MB to disk and took
1.6 s. The CTE cannot keep one visited set for the whole walk: `UNION` stops
a cycle only without a hop column, and SQLite does not allow aggregates in
the recursive part ([SQLite](https://www.sqlite.org/lang_with.html),
[PostgreSQL](https://www.postgresql.org/docs/current/queries-with.html)). Hop
by hop is also what ADR 162 already decided, it applies the per-hop limits,
and it works the same in both dialects. A closure table is worse: for a DAG
it can grow with the square of the units, and AiiDA measured 226 million
rows (200 GB) for 1 million nodes, then moved to recursive queries
([AiiDA 1.0](https://arxiv.org/pdf/2003.12476)).

### Why no graph database

The queries are short walks (at most 5 hops) from one unit with hard limits,
over at most a few million edges. SQL with a key on the source and on the
dependent answers each hop as one range read, as measured. A graph database
would be a second store to back up, replicate (ADR 248), fence and authorize,
and it would hold a second copy of the access scope. Measurements of others
point the same way: a recursive query in PostgreSQL over 131,070 reachable
nodes took 299 ms, and Apache AGE was 1.4 to 2.1 times slower than the plain
recursive query on the same data
([exobench](https://exobench.ai/blog/pg19-graph-queries-part-1)). A graph
database starts to pay for itself with long shortest-path queries over large
graphs. ADR 162 limits a path to 6 hops.

## 3. Prior art

| Product | Edges | Declared or inferred | Evidence or confidence | Storage and limits | Hidden nodes |
| --- | --- | --- | --- | --- | --- |
| [Spacelift stack dependencies](https://docs.spacelift.io/concepts/stack/stack-dependencies) | Stack to stack, optional output-to-input references | Declared | None | Must be a DAG. A delete of a stack with dependents is refused | Not documented |
| [env0 workflows](https://docs.envzero.com/guides/admin-guide/workflows/workflow-file-reference) | `needs` between sub-environments | Declared | None | Not documented | Not documented |
| [Terragrunt](https://docs.terragrunt.com/reference/hcl/blocks/) | `dependency` (outputs) and `dependencies` (order) | Declared in files | `mock_outputs` only | A DAG of the run queue. `terragrunt dag graph` prints DOT | Local files, no access model |
| [HCP Terraform run triggers](https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/run-triggers) | Source workspace to workspace | Declared | None | Up to 20 sources for each workspace | Admin on the target, read runs on the source |
| [Pulumi resource search](https://www.pulumi.com/docs/pulumi-cloud/insights/search/) | Search over resources, `dependency:` and `parent.urn:` fields | From state | None | 10,000 rows in the UI | "Users can only query resources they have access to" |
| [Pulumi Context API](https://www.pulumi.com/docs/insights/context-api/) | Resource reference, inferred reference, parent, provider, stack output use | Both | Inferred references are "leads to verify". `resultMode` exact or truncated. `visibility` complete or trimmed | Not published | Trimmed by access, and said so on the answer |
| [Backstage relations](https://backstage.io/docs/features/software-catalog/life-of-an-entity) | `dependsOn` and `dependencyOf` pairs | Declared, emitted by processors | None | A `relations` table, merged into each entity by stitching ([schema](https://raw.githubusercontent.com/backstage/backstage/master/plugins/catalog-backend/migrations/20210302150147_refresh_state.js)) | Not documented |
| [Datadog service map](https://docs.datadoghq.com/tracing/services/services_map/) | Service calls | Inferred from traces | Call counts | A node ages out after 30 days with no traces | Not documented |
| [Grafana Tempo service graph](https://grafana.com/docs/tempo/latest/metrics-from-traces/service_graphs/) | Client to server span pairs | Inferred | Request counts, unpaired and dropped counters | Metric series for each edge. `max_items` 10,000 | Not documented |
| [Powerpipe graph and edge](https://powerpipe.io/docs/powerpipe-hcl/edge) over Steampipe | Any SQL that returns `from_id` and `to_id`, often a join on ARNs | Inferred at query time, not stored | None | None | The cloud credentials of the plugin |
| [GitHub dependency graph](https://docs.github.com/en/rest/dependency-graph/dependency-submission) | Package to package from manifests and submitted snapshots | Parsed and submitted | `direct` or `indirect` | The latest snapshot for each `correlator` replaces the older ones. No storage design found | Repository access |
| [Overmind blast radius](https://docs.overmind.tech/creating-changes/blast-radius) | Live cloud relations, starting from a Terraform plan | Inferred from cloud API reads | Risks with a severity and reasons. No confidence on an edge | `--blast-radius-link-depth` and `--blast-radius-max-items` (examples 2/200 and 5/1000) | Incomplete when a source lacks permission |

What this RFC takes from them:

- **Pulumi Context API:** inferred edges are "leads", and each answer says
  whether access trimmed it. ADR 162 already has `hidden`. This RFC adds
  `confidence` on each edge, `dismissed`, `notFullyIndexed`, `indexBehind`
  and `inferredScope`.
- **GitHub snapshots:** the newest snapshot of a source replaces the old
  edges of that source. This RFC does the same for each unit with the latest
  state item.
- **Overmind:** start from the plan, not from the unit, and bound the walk
  by depth and count.
- **Steampipe:** an ARN join finds real relations, but at query time over
  the full cloud it costs a scan. This RFC precomputes the join at write time
  and stores 8-byte hashes.
- **Spacelift and HCP Terraform:** declared edges stay the exact base. ADR
  267 and ADR 313 already give them. Like Spacelift, only an exact edge
  refuses a delete.
- **Backstage and Grafana:** one row for each relation, with no closure,
  is enough for short walks.

## 4. Authorization: filter at the store

A graph answer must never name a unit that the caller cannot read, must not
show the structure behind it (ADR 162 decision 10), and must not let a writer
test whether a unit or a resource exists outside their access.

1. **The store takes the read scope.** The graph service asks
   `identity.Authorizer.ListObjects(principal, unit:read)` one time for each
   query. The answer is all, a list of namespaces, a list of collections and
   a list of units (the type already exists for the deleted-units list). The
   store gets it as JSON text and puts it in the `WHERE` of each hop. The
   store never returns an edge whose other unit is outside the scope.
2. **The filter reads live data, not the projection.** The collection of a
   unit comes from `collection_members` in the query, so a move between
   collections applies at once. A namespace link and a sharing rule are
   checked in the query, so an unlink applies at once, before `graph.relink`
   removes the rows. The projection gives only the shape of the graph, never
   an input of authorization.
3. **The batch check stays as a second layer.** Before the walk follows a
   unit, `graph.Access.Readable` checks the batch, as today. It covers what
   a scope list cannot say: grant conditions such as expiry (ADR 313) and the
   Cedar access limits (ADR 223). A unit that fails either layer is hidden.
   Each layer has a comment that names the other and a test that sends a
   scope or a batch that the other layer would have stopped.
4. **A limit on the scope.** A scope with more than 10,000 entries (for
   example a principal with grants on thousands of units) is refused with an
   error, and the denial is in the audit log. The PM or the owner can raise
   the limit, but it never becomes unbounded.
5. **`hidden` counts distinct units behind exact edges only.** The store
   returns the opaque IDs of hidden units, and the walk dedupes them across
   hops and both layers. An inferred edge (`identity`, `state` not verified,
   `lock`) to a unit that the caller cannot read is left out, and is not
   counted. Each answer says so with the constant `inferredScope:
   readable-units-only`. The text never changes with the data, so it is not
   an oracle. Without this rule, a writer could plant a guessed ARN and see
   `hidden` change.
6. **No route takes a free value.** The plan impact hashes values that the
   server read from a unit that the caller can read. The value view takes a
   value of a unit that the caller can read. A planted value can make an edge
   only to a unit that the caller can already read (rule 5), so the walk
   tells the planter nothing new.
7. **Across namespaces only with consent.** Identity lookups read the
   namespace and its live linked namespaces only (ADR 163 decision 9). A
   state reference across namespaces needs a live link or a sharing rule of
   the producer (section 1).
8. **Fail closed.** An error from `ListObjects`, an unknown scope kind or an
   empty answer makes an empty scope: the start unit answers 404 (ADR 166
   decision 14), and a denied read appends the entry of a denied request.
9. **The evidence route** needs the read of the state of both units, because
   it shows resource keys and values from both.

## 5. ADR changes

If the owner accepts this RFC, its ADR changes these decisions:

| ADR | Decision | Change |
| --- | --- | --- |
| 162 | 1, nodes and edges | An edge is a dependency edge of ADR 153 or a derived edge (`state`, `identity`) |
| 162 | 4, no second record | `graph_derived_edges` is a second edge store, a projection of state. `graph.EdgeReader` reads both stores. The dependency port gets no write for derived edges |
| 162 | 8, the window | A derived edge has no window. It exists while the two latest states match |
| 162 | 9, limits | The cut of a hop orders by confidence first |
| 162 | 10 and 12, access and `hidden` | The store filters by the read scope. `hidden` counts distinct units behind exact edges only. New fields: `notFullyIndexed`, `indexBehind`, `dismissed`, `inferredScope` |
| 162 | 13, across namespaces | A derived edge is in the namespace of its dependent, written under its fence. The edges of ADR 153 stay in the namespace of the source |
| 153 | 5, which edges block | Adds verified `state` edges. Keeps or removes non-ambiguous `lock` (owner question 3) |
| 163 | 3, key rules | Adds the `id` of a resource as a produced value |
| 166 | 16, `dependency.Store.Edges` | The method takes the read scope and returns hidden unit IDs. The access filter is in the store, with `graph.Access` as a second layer |
| 166 | 17 and 18, indexes and conformance cases | Adds the derived edge table, its indexes and the cases of section 2 (hostile inputs, generations, tombstones) to `dependencytest` and `graphtest` |
| 194 | 7, the blast radius in a review | The view shows the confidence mark of each edge and the new completeness fields |
| 226 | the fact group `dependents` | States that `count` and `units` hold only the edges that block (no inferred edge) |

## 6. One-way doors

Before the first tag, the schema baseline changes in place (ADR 322), and
the tool-neutral API can break (ADR 73). The doors that stay hard to close:

1. **An inferred edge that blocks or denies.** Once teams depend on a delete
   or a gate that a guess blocks, a later change to "warn only" reads as a
   removed safety check. This RFC keeps inferred edges out of blocks and out
   of gate facts.
2. **A lookup across namespaces.** If one release answers whether a unit or
   a value of another namespace exists, that knowledge is out, and no later
   release takes it back. This RFC needs consent (a link or a sharing rule),
   and gives one number for "not found" and "not readable".
3. **The hash leaves the server.** If a hash is in an API, an export or a
   log, the key cannot change without a break. This RFC keeps the hash
   internal and the tables in the projection class.
4. **The index enters the backup.** A backup with the index holds a second
   copy of state values for as long as the backup lives. This RFC keeps the
   index out of every backup and rebuilds it.
5. **The names of evidence and confidence** in the API, once the console and
   clients filter on them: `job`, `lock`, `declared`, `state`, `identity`,
   then `exact`, `strong`, `weak`, `hint`.
6. **`WITHOUT ROWID` in the SQLite baseline.** The tables are a projection,
   so a change is a rebuild. But it is the first use of the pattern, and
   other areas may copy it.

The row layout, the hash form, the allowlist, the limits, the chunk size and
the GIN option are two-way doors: the index is a projection, and a change is
a rebuild.

## 7. Slices

Each slice lands with its e2e step first, through the real CLI.

1. **Measure and benchmark.** A corpus tool reads the state fixtures of the
   e2e suite and the states that an operator gives on the command line, and
   prints the produced and consumed values for each resource (only the
   counts). The benchmark of this RFC in `bench/graphindex/` (one data set,
   both dialects, cold and warm, churn, a hub run, an extraction) moves into
   `make bench`. No API
   change. The result replaces the range of 1 to 5 consumed values.
2. **State references.** Extraction of `pulumi:pulumi:StackReference`,
   `terraform_remote_state` and `tfe_outputs` in the projection run,
   `graph_unit_refs`, `graph_derived_edges` with the evidence `state`, rule 1
   to 3 of section 1, and "references not shown". **e2e:** the Pulumi CLI
   runs `pulumi up` on stack B with a `StackReference` to stack A, as a
   principal that can read A. The downstream graph of A lists B with
   `evidence: state`, `verified: true` and `confidence: exact`. OpenTofu
   applies workspace B with a `terraform_remote_state` on the `remote`
   backend of this server. The same check applies. A `tofu state push` into
   B by a principal with no read of A gives `verified: false`, and a delete
   of A is not blocked. A push that names a unit of another namespace with no
   link gives no edge.
3. **The value index and identity edges.** `identity_produced`,
   `identity_consumed`, `identity_set_chunks`, the diff write, the
   generations, the limits, the evidence route and dismissals. **e2e:**
   `tofu state push` of a fixture state with an `aws_vpc` into workspace A
   and a fixture with an `aws_subnet` whose `vpc_id` is that VPC into
   workspace B. The downstream graph of A lists B with `evidence: identity`,
   `confidence: weak` and `matches: 1`. The evidence route shows the pair. A
   dismissal hides the edge from the walk and counts it. `pulumi stack
   import` of a fixture with an ARN gives `strong`. A fixture with 10^5
   guessed ARNs gives at most 500 edges and leaves the exact edges of the
   answer unchanged.
4. **The scope filter in the store.** The read scope in the edge query of
   both tables, live membership and links, hidden unit IDs from the store,
   and the batch check kept. **e2e:** a principal with `unit:read` on one
   collection only walks from a unit of it. The answer names no unit of the
   other collection, `hidden` counts only the units behind exact edges, and
   a move of a unit out of the collection hides it in the next answer, with
   no new state item.
5. **Plan impact.** `POST /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/graph/plan-impact`
   with a stored plan ID. **e2e:** a speculative plan of workspace A that
   removes an output that workspace B reads through `terraform_remote_state`
   lists B with the reason `output`. A fixture state and a plan with a
   replaced `aws_vpc` list the consumer with the reason `value`.
6. **Across linked namespaces.** The `graph.relink` operation and the edge
   row in the namespace of the dependent. **e2e:** two namespaces with a
   link, fixtures pushed into each, the edge shows. After an unlink, the next
   answer leaves it out at once.
7. **Profiles and the value view.** A profile as the anchor of a blast radius
   (the units that attach it and the profiles that import it), and the value
   view of one value. **e2e:** two units attach one profile. The blast radius
   of the profile lists both.
8. **DNS names** (later), with the confidence `hint`.

## Open questions for the owner

Each question stands alone. Each has options, a recommendation and a short
explanation for a reader who is new to the topic (ELI5).

1. **What does a blast radius walk follow by default?**
   ELI5: the server can be sure that B uses A (B read A's outputs), or it can
   guess (B holds the ID of a network that A made). When you ask "what breaks
   if I change A", should the answer include the guesses?
   - A. Exact edges only. Guesses show only when the person asks for them.
     Consequence: a dependent that is known only by a shared ID, for example
     a VPC, is not in the answer unless the person asks for guesses.
   - B. Exact edges and the `strong` and `weak` guesses, each with a mark.
     `hint` edges (ambiguous locks, DNS) only on request.
   - C. Everything, `hint` included.
     Consequence: ambiguous lock edges and DNS edges add units that often do
     not depend on A.

   Recommendation: B. A missed dependent is the costly error in a blast
   radius, and the mark keeps the guess honest. Profiles are an anchor in all
   three options: "what breaks if I change this profile" lists the units that
   use it, with no edges between those units.

2. **When can an edge cross from one namespace to another?**
   ELI5: two teams in two namespaces can use one cloud account, and a unit of
   one team can name a unit of the other. If the server joins them without
   asking, a team can learn what the other team has.
   - A. Never.
     Consequence: two teams in two namespaces that share a VPC never see each
     other in a blast radius, also with a link.
   - B. An identity match needs a namespace link that both namespaces accept
     (ADR 216). A state reference needs that link or a sharing rule of the
     producer (ADR 273).
   - C. Always, and the walk hides the units that the caller cannot read.

   Recommendation: B. Option C lets a writer test the names of other teams.

3. **What may block a delete, and what may a gate read?**
   ELI5: today the server stops a delete of A when it saw B read A, or when
   it thinks B read A because the reader held B's lock. Now the server also
   knows edges from state, some of them guesses.
   - A. Keep ADR 153 as it is (job, declared and non-ambiguous lock edges
     block), and add verified state references. No inferred edge enters the
     gate fact `dependents`. Inferred edges warn.
   - B. Like A, but non-ambiguous `lock` edges stop blocking and only warn,
     because they are an inference too.
     Consequence: a delete that a non-ambiguous lock edge blocks today goes
     through with a warning.
   - C. Every edge blocks and enters the gate facts.

   Recommendation: A now, and B after the e2e suite shows how often `lock`
   edges are wrong. Option C lets one writer stop the work of another team
   with a planted value.

4. **Who can dismiss a wrong guess?**
   ELI5: sometimes the guess is wrong, and a person hides that one edge with a
   reason. Everyone can see who hid it and why. A new action
   `unit:manage-edges` would hold the permission.
   - A. A person who can change the dependent unit (the unit that holds the
     value).
     Consequence: the team that made the resource cannot hide a wrong guess
     about its unit.
   - B. A person who can change the source unit (the unit that made the
     resource).
     Consequence: the team that holds the value cannot hide a wrong guess on
     its unit.
   - C. Either of them.
   - D. Both of them must agree.

   Recommendation: C. Each side knows its half of the truth, and the record
   shows who did it. D makes a fix wait on two teams.

5. **Do you accept the ADR changes in section 5?**
   ELI5: the accepted graph design reads only one table of edges, has no
   edges from state, filters access after the read, and counts every hidden
   unit. This RFC adds a second, derived table, filters access in the
   database with live data, and stops counting hidden units behind guesses,
   so the count cannot leak them.
   - A. Accept all of them.
   - B. Accept them, but keep the access filter after the read (ADR 166
     decision 16 as it is), with only the live data in the batch check.
   - C. Accept only the exact state references now, and leave identity
     matches for a later RFC.

   Recommendation: A. B keeps one layer of the two that ADR 250 asks for. C
   leaves the shared VPC case unsolved.

6. **Which storage layout and budget?**
   ELI5: the index costs about 90 to 200 bytes for each cloud resource in
   SQLite and about 490 to 1,070 bytes in PostgreSQL, after churn. That is
   about 1% to 5% (SQLite) and 7% to 27% (PostgreSQL) of one copy of the
   state that it reads. One million resources cost about 90 to 200 MB in
   SQLite and 0.5 to 1.1 GB in PostgreSQL. The range comes from how many IDs
   each resource uses, and the first slice measures that on real states.
   - A. Rows in both dialects, and `WITHOUT ROWID` tables in SQLite (the
     first in the baseline). The sizes above.
   - B. Like A, but SQLite keeps rowid tables like the rest of the baseline.
     About 2.8 times larger in SQLite after churn, and the file keeps growing.
   - C. Like A, and in PostgreSQL a list of IDs for each chunk with a GIN
     index on the consumed side. About 3.7 times smaller on that side, but
     the two dialects then have different query types (ADR 274).

   Recommendation: A, and C only if the first slice measures more than 4
   consumed IDs for each resource on real states.

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). The index is a
  projection that each installation builds. A cross-namespace edge is
  written by the dependent namespace under its own fence, from a durable
  operation of that namespace.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
