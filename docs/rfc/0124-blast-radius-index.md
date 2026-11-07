---
rfc: 124
title: An index of the unit graph and the blast radius that is fast and small
status: discussing
created: 2026-11-07
---

# RFC 124. An index of the unit graph and the blast radius that is fast and small

Status: discussing. Design only. No code changes with this RFC. Until the
owner decides, ADRs 153, 162, 163 and 166 stay as they are.

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

## What this RFC answers

1. The edges that exist, where each comes from, and how each carries its
   confidence and its evidence.
2. The index: what it stores, when it changes, how it stays small, its cost
   per 1,000 units and per 1,000,000 resources, and the query plans in both
   SQL dialects.
3. Prior art, with sources.
4. Authorization: the store filters by the read scope of the caller.
5. One-way doors, slices with e2e steps, and the questions for the owner.

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
| **State reference** (new), evidence `state` | The latest state of the dependent holds a reference to the source by name: a Pulumi `StackReference`, an OpenTofu `terraform_remote_state` or `tfe_outputs` data source that names a unit of this installation | Exact | No |
| **Identity match** (new), evidence `identity` | A value in the latest state of the dependent is equal to an identity value of a resource that the source manages | Inferred | No |
| Profile attachment (anchor, not an edge) | A unit attaches a profile (ADR 203) | Exact | Profile attachments |
| Managed twice (link, not an edge) | Two units manage one resource (ADR 163, ADR 165) | Exact | Resource index port |

### State references, exact

These come from the state of the dependent, so they need no job, no lock and
no window. The state shapes, from the source code of each tool:

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

A name that does not resolve is not an edge. The unit shows the count of
**unresolved references**, so a person sees that a reference points to
another server or to a unit that does not exist.

### Identity matches, inferred

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

A shared VPC, subnet or security group needs no special case. The unit that
manages the VPC produces its ID, and each unit that puts a subnet in it
consumes the ID. Two units that consume the same VPC do not get an edge
between them: they are siblings, not dependents. A VPC that no unit manages
makes no edge. The console can show the units that use one value (the
**value view**, slice 7), but a walk does not follow it.

DNS names (a load balancer name in the configuration of another unit) are a
later slice, with a weak confidence. A DNS name can point to many things, and
a name that a unit uses can be a CNAME of a name that another unit makes.

### Confidence and evidence ("No magic")

Confidence is not a stored score. It is a function of the evidence, in one
table in the catalog of code rules, and the API gives both:

| Evidence | Confidence | Why |
| --- | --- | --- |
| `job`, `declared`, `state` | `exact` | The server saw the read, a person declared it, or the state names the unit |
| `identity` on an ARN, an Azure resource ID or a Google Cloud full name | `strong` | The value names one resource in one account or project |
| `identity` on a short ID (`subnet-…`, `sg-…`, `vpc-…`) | `weak` | The format is unique in practice, but AWS documents it only in an account and a region |
| `lock`, not ambiguous | `weak` | ADR 153: an inference from the lock |
| `lock`, ambiguous, and DNS (later) | `hint` | Listed, never followed by default |

Each edge in an answer holds:

- `evidence`, `confidence`, `firstSeen` and `lastSeen`,
- for `identity`: `matches` (the count of distinct values) and one
  **example**: the value, the resource key in each unit and the attribute
  path in the dependent, for example
  `vpc-0e9801d129example: module.vpc.aws_vpc.main → aws_subnet.a .vpc_id`,
- for `state`: the resource key of the reference in the dependent.

A route `GET .../graph/edges/{source}/{dependent}/evidence` lists up to 50
value pairs of one edge. It reads the latest masked state of each unit, and
needs the read action of the state of both units. The index itself stores no
list of pairs.

A person can **dismiss** an inferred edge with a reason. A dismissal is a
stored record (holder, edge, reason, person, time), listed and revocable,
in the backup. A walk does not follow a dismissed edge, and the answer counts
it in `dismissed`. A person can also declare an edge (ADR 313).

An identity edge never blocks a delete (owner question 3). It is listed next
to the delete check, like an ambiguous lock edge (ADR 153).

## 2. The index

### What to store

Six projection tables and one data table. The projection tables are in
`backup.ProjectionTables` (ADR 166 decision 5): a backup leaves them out, and
a restore rebuilds them from the latest state items. So their layout is a
two-way door: a change is a rebuild, not a migration of data.

| Table | Row | Key | Class |
| --- | --- | --- | --- |
| `graph_unit_refs` | one for each unit: a local integer `unit_seq` for (namespace ID, tool, unit ID), with `ns_seq` and the current `collection_id` | `unit_seq`; unique (namespace ID, tool, unit ID) | projection |
| `graph_namespace_heads` | one for each namespace: `ns_seq` for the namespace ID. Its row is the graph lock of the namespace | `ns_seq`; unique namespace ID | projection |
| `identity_produced` | one for each distinct value that a unit produces | (`ns_seq`, `value_hash`, `unit_seq`) | projection |
| `identity_consumed` | one for each distinct value that a unit consumes | (`ns_seq`, `value_hash`, `unit_seq`) | projection |
| `identity_unit_sets` | one for each unit: its two value sets as sorted 8-byte arrays, the state order, the counts, `complete` | `unit_seq` | projection |
| `graph_derived_edges` | one for each (source, dependent, evidence) of the kinds `state` and `identity` | (`source_seq`, `dependent_seq`, `evidence`); index (`dependent_seq`, `source_seq`) | projection |
| `graph_edge_dismissals` | one for each dismissal | (namespace ID, source, dependent, evidence) | data, backed up |

The reverse index is `identity_produced` and `identity_consumed`: from a value
to its units. The key leads with the namespace, so each lookup stays in one
namespace (and its linked namespaces), and a purge of a namespace is one
range delete.

The edges of ADR 153 stay in their own table: they are recorded facts, not a
projection, and a backup holds them. A hop reads both tables (the graph
adapter makes one `UNION ALL`).

### How to keep it small

Each choice below was measured (see "Measurements").

1. **Hash the values.** `value_hash` is the first 8 bytes of
   HMAC-SHA-256 of the normalized value, with a key that the installation
   derives from its secret keys. The standard library has HMAC, so no new
   dependency. An attacker who writes state cannot make a collision with a
   value of another unit without the key. The chance of one random collision
   among 10^8 values is about 2.7 × 10^-4
   ([birthday bound](https://preshing.com/20110504/hash-collision-probabilities/)).
   A collision makes at most one false inferred edge, and the evidence route
   shows the real values, so a person sees it. The hash never leaves the
   server: no API, export or log holds it, so the key can change with a
   rebuild.
2. **Store only identity-shaped values.** The allowlist below keeps about 2
   produced and an estimated 1.5 distinct consumed values for each resource,
   not the 20 to 100 attributes of a resource.
3. **Dedupe for each unit.** A unit with 300 subnets that all use one VPC ID
   consumes the value one time. Edges are between units, so the resource that
   holds a value is not in the row. The evidence route finds it in the state.
4. **Intern the long keys.** A unit reference is a namespace ID, a tool and
   a unit ID, about 60 bytes of text. Rows hold an 8-byte `unit_seq` and a
   4-byte `ns_seq` from `graph_unit_refs`. The integers are local to one
   installation. This is safe because the tables are a projection: a restore,
   a replica installation (ADR 248) or a new key rebuilds them.
5. **Per-unit sets for the diff.** The run must know the old values of a unit
   to delete only what changed (ADR 163 decision 5). A second index on
   (`unit_seq`, `value_hash`) costs about 20 bytes in SQLite and 36 bytes in
   PostgreSQL for each row. A sorted 8-byte array in `identity_unit_sets`
   costs 8 bytes, and the run reads it in one row.
6. **Only the latest state.** The index holds the latest state item of each
   unit, never the history. A point-in-time graph reads history (ADR 150), not
   this index.
7. **Partition by namespace through the key**, not by a partitioned table.
   Every row leads with `ns_seq`. Both dialects then read one namespace as one
   key range, and a purge deletes one range.
8. **Compaction on each state version** is the diff write: the run deletes
   the values that the new item no longer holds, inserts the new ones, and
   recomputes the edges of the unit. No row of an old item stays. PostgreSQL
   autovacuum takes back the space of the deleted rows.

### Extraction rules (security code)

The state is untrusted input (ADR 250). The rules:

1. **Allowlist at the boundary.** A consumed value is kept only when it
   matches one shape, else it is dropped:
   - ARN: `arn:<partition>:<service>:<region>?:<account>?:<resource>`, with a
     known partition, at most 2,048 bytes.
   - AWS short ID: a prefix from a fixed list (`vpc`, `subnet`, `sg`, `igw`,
     `nat`, `rtb`, `eni`, `i`, `vol`, `tgw`, `vpce`, `pcx`, ...) and 8 or 17
     lowercase hex characters. The lengths are those of
     [the EC2 ID formats](https://aws.amazon.com/blogs/compute/longer-resource-ids-in-2018-for-amazon-ec2-amazon-ebs-and-amazon-vpc).
   - Azure: `/subscriptions/<GUID>/resourceGroups/...`, normalized to
     lowercase with no trailing slash (ADR 163 decision 2).
   - Google Cloud: `projects/...` full names, and self links on
     `www.googleapis.com` or `compute.googleapis.com`, normalized to the
     `projects/...` form.
2. **Contain.** The value goes into the HMAC and the hash goes into a
   placeholder. No value reaches SQL text, a log line or an error.
3. **Limit.** At most 64 consumed values for each resource, 200,000 distinct
   values for each unit, a JSON scan of at most 64 KiB and a depth of 8 for
   one string, and a time budget for each run. A unit above a limit indexes
   the first values by hash and gets the mark "not fully indexed" (ADR 163
   decision 7).
4. **Authorize at the point of use.** See section 4.
5. **Fail closed.** A state that does not parse makes no values and no edges,
   marks the unit "not fully indexed", and the run records the reason. It
   never keeps the edges of an older item as if they were current.

Secret values never reach the index: the run reads the masked view (ADR 41),
and a masked attribute has no value to match. Client-encrypted state has no
values (ADR 163 decision 6).

Each step gets its own test with hostile input, as ADR 250 asks: a
2-MiB ARN, a JSON bomb in a policy, 10^6 values in one resource, a value
that looks like a short ID with upper case, a state that does not parse, and a
caller scope that covers nothing.

### When the index changes

**As a durable follow-up, not in the transaction of the state write.** A
Pulumi checkpoint or an OpenTofu state version is on the hot path of the CLI.
A parse of a 100-MB state in that transaction would hold it for seconds. The
index changes in the projection run of ADR 166 decision 7, which already
reads each latest state item one time, masked, coalesced for each unit. The
steps of that run, extended:

1. Read the latest masked item (as today).
2. Context and resource index (as today).
3. **Values.** Extract the produced and consumed sets, read the old sets
   from `identity_unit_sets`, and write the difference to
   `identity_produced` and `identity_consumed`. Write the new sets. One
   transaction, with the fence of the namespace.
4. **Edges.** In a new transaction, take the **graph lock of the
   namespace** (a row lock on `graph_namespace_heads` in PostgreSQL, while SQLite
   has one writer). Then recompute the edges where the unit is the source or
   the dependent:
   - downstream: look up the produced hashes of the unit in
     `identity_consumed`, in batches of 1,000.
   - upstream: look up its consumed hashes in `identity_produced`.
   - state references: resolve the names.
   - write the difference to `graph_derived_edges`.

   The lock makes the result correct under concurrent runs: each run commits
   its values before it takes the lock, so the last run to hold the lock sees
   the committed values of both units of each edge.
5. **Across linked namespaces.** An identity edge between two namespaces
   exists only with a namespace link (ADR 163 decision 9, ADR 216). The edge
   row lives in the namespace of the dependent, written with its fence, so no
   run writes into the half of another namespace (ADR 248). When a run finds
   consumers in a linked namespace, it starts a durable operation
   `graph.relink:<namespace ID>` for that namespace, with one start key for
   each namespace, so the operations coalesce like the projection run. That
   operation recomputes the upstream edges of the found units under the fence
   and the graph lock of their namespace.

Freshness: an answer that names a unit whose projection is behind its latest
state item (ADR 166, `ErrIndexBehind`) counts it in a new completeness field
`indexBehind`. A soft delete keeps the derived edges with the deleted mark,
so the blast radius of a restore is known. A purge drops them.

A derived edge has no window (ADR 162 decision 8): it exists while the two
latest states match. `firstSeen` and `lastSeen` are the times of the runs
that first and last confirmed it. The record of each run holds the counts of
edges added and removed.

### Measurements

Synthetic data with the shape above: 3,334 units of 300 resources each
(1,000,200 resources) in 10 namespaces, 2 produced values for each resource,
about 1.5 distinct consumed values for each resource, 70% of them from one
hub unit (a network unit) for each consumer. Hardware: a container with 4
vCPUs (Intel Xeon at 2.1 GHz), warm cache. PostgreSQL 18.6 and SQLite 3.53.1
with `WITHOUT ROWID` tables.

| Table, 1,000,200 resources | Rows | PostgreSQL | Bytes/row | SQLite | Bytes/row |
| --- | --- | --- | --- | --- | --- |
| Produced values, hashed | 2,000,400 | 217 MB | 114 | 41.7 MB | 20.8 |
| Consumed values, hashed | 1,484,931 | 149 MB | 105 | 29.2 MB | 19.6 |
| Produced values, as text (the naive form) | 2,000,400 | 743 MB | 390 | 320 MB | 160 |
| Consumed values, PostgreSQL `int8[]` for each unit with a GIN index | 1,484,931 values | 40 MB | 28 | n/a | n/a |
| Per-unit sets (8 bytes for each value) | 3.5 M values | ~28 MB | 8 | ~28 MB | 8 |
| Edges, with the dependent index | 43,197 (SQLite), 493,288 (PostgreSQL, a denser variant) | 71 MB | ~150 | 1.8 MB | ~41 |

The hash saves a factor of 3.4 in PostgreSQL and 7.7 in SQLite against text.

**Storage estimate.** The value count for each resource (2 produced, 1.5
consumed) is an estimate, and slice 1 measures it on real states (owner
question 6). With it:

| Size | Resources | PostgreSQL | SQLite |
| --- | --- | --- | --- |
| Per resource | 1 | ~415 B | ~100 B |
| 1,000 units of 100 resources | 100,000 | ~42 MB | ~10 MB |
| 1,000 units of 300 resources | 300,000 | ~125 MB | ~30 MB |
| 1,000,000 resources | 1,000,000 | ~415 MB | ~100 MB |

Plus the edges: at 10 edges for each unit, 1,000 units have 10,000 edges,
about 1.5 MB in PostgreSQL with the example text.

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

So the index is about 6% to 10% of one copy of the latest state in
PostgreSQL and 1.4% to 2.5% in SQLite. The server keeps many state versions,
so against all stored state the share is smaller. The PostgreSQL row overhead
(a 23-byte tuple header and a 4-byte item pointer for each heap row, and
about 20 bytes for each B-tree entry,
[page layout](https://www.postgresql.org/docs/current/storage-page-layout.html))
is most of the 105 to 114 bytes. If that matters, the consumed side can move
to one `int8[]` for each unit with a GIN index (measured: 28 bytes for each
value, 3.7 times smaller). The cost: a change writes the whole array, so the
diff write of ADR 163 decision 5 is lost, and ADR 274 asks that both
dialects keep the same query types. So this RFC keeps rows, and names the
array as a later, PostgreSQL-only change that a rebuild applies.

### Query plans

The graph keeps the walk of ADR 162: hop by hop in Go, with a visited set,
one batch of at most 500 units for each hop, at most 5,000 edges for each
hop. Each hop is one statement with the scope filter in it (section 4).

**"What depends on X", one hop downstream, PostgreSQL.** The frontier is
`$1`. The scope is `$2` (all), `$3` (namespace sequences), `$4` (collection
IDs) and `$5` (unit sequences):

```sql
SELECT e.source_seq, e.dependent_seq, e.evidence, e.confidence,
       e.matches, e.last_seen_ms
FROM graph_derived_edges e
JOIN graph_unit_refs s ON s.unit_seq = e.source_seq
JOIN graph_unit_refs d ON d.unit_seq = e.dependent_seq
WHERE e.source_seq = ANY($1::bigint[])
  AND e.deleted = 0
  AND ($2 OR d.ns_seq = ANY($3::int[])
          OR d.unit_seq = ANY($5::bigint[])
          OR d.collection_id = ANY($4::text[]))
  AND NOT EXISTS (SELECT 1 FROM graph_edge_dismissals x
                  WHERE x.namespace_id = d.namespace_id
                    AND x.source_tool = s.tool AND x.source_unit_id = s.unit_id
                    AND x.dependent_tool = d.tool AND x.dependent_unit_id = d.unit_id
                    AND x.evidence = e.evidence)
ORDER BY e.last_seen_ms DESC, e.source_seq, e.dependent_seq
LIMIT 5001;
```

The same statement with `NOT (...)` on the scope and `count(*)` gives
`hidden`, in the same read transaction. The plan is a bitmap index scan on
the primary key `(source_seq, dependent_seq, evidence)`, a hash join to
`graph_unit_refs`, and a top-N sort. Upstream uses the index
`(dependent_seq, source_seq)`. `collection_id` is a column of
`graph_unit_refs` that the projection keeps current, so the filter needs no
join to the collections. The dismissals keep the stable IDs, because a
backup holds them, so the query joins them through `graph_unit_refs`.

**SQLite.** The same statement, with `IN (SELECT value FROM json_each(?1))`
for each array, so the query is static and sqlc generates it. The plan is
`SEARCH graph_derived_edges USING PRIMARY KEY (source_seq=?)` for each unit
of the frontier, then a temporary B-tree for the order.

**"The blast radius of this plan".** The input is a stored plan (RFC 28) or
the plan of a job, never a value from the caller:

1. From the plan, the resources with the action delete or replace, and the
   outputs that the plan removes or changes.
2. The server reads the identity values of those resources from the latest
   state of the unit, and hashes them.
3. Direct hits, with the reason for each unit:

   ```sql
   -- PostgreSQL. $1: the namespace and its live linked namespaces.
   -- $2: at most 1,000 hashes for each call.
   SELECT c.unit_seq, count(*) AS hits
   FROM identity_consumed c
   JOIN graph_unit_refs d ON d.unit_seq = c.unit_seq
   WHERE c.ns_seq = ANY($1::int[])
     AND c.value_hash = ANY($2::bigint[])
     AND (<the scope filter on d, as above>)
   GROUP BY c.unit_seq;
   ```

   SQLite: `c.ns_seq IN (SELECT value FROM json_each(?1)) AND c.value_hash IN
   (SELECT value FROM json_each(?2))`, plan `SEARCH identity_consumed USING
   PRIMARY KEY (ns_seq=? AND value_hash=?)`.

   Plus, when an output changes, the units with an exact edge from the unit
   (output read or state reference): the server cannot tell which output
   each reader uses (ADR 153 decision 16).
4. A walk of the remaining hops (default 2 in all) from the direct hits.

The answer lists each unit with its reason (`value`, `output` or
`transitive`), its hop, and the lowest confidence on its path.

**Why not one recursive CTE.** Measured in PostgreSQL on the dense variant:
a `WITH RECURSIVE` walk of 3 hops from one unit, with a path array to stop
cycles, made 858,212 rows, wrote 69 MB to disk and took 1.6 s. The CTE
cannot keep one visited set for the whole walk: `UNION` stops a cycle only
without a hop column, and SQLite does not allow aggregates in the recursive
part ([SQLite](https://www.sqlite.org/lang_with.html),
[PostgreSQL](https://www.postgresql.org/docs/current/queries-with.html)). Hop
by hop is also what ADR 162 already decided, it applies the per-hop limits,
and it works the same in both dialects. A closure table is worse: for a DAG
it grows with the square of the units in the worst case, and AiiDA measured
226 million rows (200 GB) for 1 million nodes, then moved to recursive
queries ([AiiDA 1.0](https://arxiv.org/pdf/2003.12476)).

**Measured times** on the synthetic data:

| Query | PostgreSQL | SQLite |
| --- | --- | --- |
| Plan hits: 1,000 hashes of one hub unit to their consumers | 4.6 ms | 1.5 ms |
| One hop downstream, frontier of 500 units, scope filter, cut at 5,001 edges | 52 ms (76,448 candidate edges) | 3.9 ms |
| Recursive CTE, 3 hops, path array | 1,612 ms | not measured |
| Build all edges of the data from scratch (rebuild) | 3.3 s | 0.5 s |

A walk of 5 hops is then at most 5 statements of the second line. These are
numbers of a dense synthetic graph. Real graphs have fewer edges for each
unit. Slice 1 adds a benchmark with these shapes to `make bench`.

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
| [Spacelift stack dependencies](https://docs.spacelift.io/concepts/stack/stack-dependencies) | Stack to stack, optional output-to-input references | Declared | None | Must be a DAG; a delete of a stack with dependents is refused | Not documented |
| [env0 workflows](https://docs.envzero.com/guides/admin-guide/workflows/workflow-file-reference) | `needs` between sub-environments | Declared | None | Not documented | Not documented |
| [Terragrunt](https://docs.terragrunt.com/reference/hcl/blocks/) | `dependency` (outputs) and `dependencies` (order) | Declared in files | `mock_outputs` only | A DAG of the run queue; `terragrunt dag graph` prints DOT | Local files, no access model |
| [HCP Terraform run triggers](https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/run-triggers) | Source workspace to workspace | Declared | None | Up to 20 sources for each workspace | Admin on the target, read runs on the source |
| [Pulumi resource search](https://www.pulumi.com/docs/pulumi-cloud/insights/search/) | Search over resources, `dependency:` and `parent.urn:` fields | From state | None | 10,000 rows in the UI | "Users can only query resources they have access to" |
| [Pulumi Context API](https://www.pulumi.com/docs/insights/context-api/) | Resource reference, inferred reference, parent, provider, stack output use | Both | Inferred references are "leads to verify"; `resultMode` exact or truncated; `visibility` complete or trimmed | Not published | Trimmed by access, and said so on the answer |
| [Backstage relations](https://backstage.io/docs/features/software-catalog/life-of-an-entity) | `dependsOn` and `dependencyOf` pairs | Declared, emitted by processors | None | A `relations` table, merged into each entity by stitching ([schema](https://raw.githubusercontent.com/backstage/backstage/master/plugins/catalog-backend/migrations/20210302150147_refresh_state.js)) | Not documented |
| [Datadog service map](https://docs.datadoghq.com/tracing/services/services_map/) | Service calls | Inferred from traces | Call counts | A node ages out after 30 days with no traces | Not documented |
| [Grafana Tempo service graph](https://grafana.com/docs/tempo/latest/metrics-from-traces/service_graphs/) | Client to server span pairs | Inferred | Request counts, unpaired and dropped counters | Metric series for each edge; `max_items` 10,000 | Not documented |
| [Powerpipe graph and edge](https://powerpipe.io/docs/powerpipe-hcl/edge) over Steampipe | Any SQL that returns `from_id` and `to_id`, often a join on ARNs | Inferred at query time, not stored | None | None | The cloud credentials of the plugin |
| [GitHub dependency graph](https://docs.github.com/en/rest/dependency-graph/dependency-submission) | Package to package from manifests and submitted snapshots | Parsed and submitted | `direct` or `indirect` | The latest snapshot for each `correlator` replaces the older ones; no storage design found | Repository access |
| [Overmind blast radius](https://docs.overmind.tech/creating-changes/blast-radius) | Live cloud relations, starting from a Terraform plan | Inferred from cloud API reads | Risks with a severity and reasons; no confidence on an edge | `--blast-radius-link-depth` and `--blast-radius-max-items` (examples 2/200 and 5/1000) | Incomplete when a source lacks permission |

What this RFC takes from them:

- **Pulumi Context API:** inferred edges are "leads", and each answer says
  whether access trimmed it. ADR 162 already has `hidden`. This RFC adds
  `confidence` on each edge, `dismissed` and `indexBehind`.
- **GitHub snapshots:** the newest snapshot of a source replaces the old
  edges of that source. This RFC does the same for each unit with the latest
  state item.
- **Overmind:** start from the plan, not from the unit, and bound the walk
  by depth and count.
- **Steampipe:** an ARN join finds real relations, but at query time over
  the full cloud it costs a scan. This RFC precomputes the join at write time
  and stores 8-byte hashes.
- **Spacelift and HCP Terraform:** declared edges stay the exact base. ADR
  267 and ADR 313 already give them.
- **Backstage and Grafana:** one row for each relation, with no closure,
  is enough for short walks.

## 4. Authorization: filter at the store

A graph answer must never name a unit that the caller cannot read, and must
not show the structure behind it (ADR 162 decision 10).

The proposal changes one point of ADR 166 decision 16 ("The access filter is
not in the store"):

1. **The store takes the read scope.** The graph service asks
   `identity.Authorizer.ListObjects(principal, unit:read)` one time for each
   query. The answer is all, a list of namespaces, a list of collections and
   a list of units (the type already exists for the deleted-units list). The
   store gets it as a value and puts it in the `WHERE` of each hop, as above.
   The store never returns an edge whose other unit is outside the scope. It
   returns only the count of such edges as `hidden`.
2. **The batch check stays as a second layer.** Before the walk follows a
   unit, `graph.Access.Readable` checks the batch, as today. It covers what
   a scope list cannot say: grant conditions such as expiry (ADR 313) and the
   Cedar access limits (ADR 223). A unit that fails either layer is hidden.
   Each layer has a comment that names the other and a test that sends a
   scope or a batch that the other layer would have stopped.
3. **No route takes a free value.** The plan impact hashes values that the
   server read from a unit that the caller can read. The value view takes a
   value of a unit that the caller can read. No route answers "which units
   hold this ARN" for a value that the caller types. So the index is not an
   oracle for the resources of other teams.
4. **Across namespaces only with a link.** Identity lookups read the
   namespace and its live linked namespaces only (ADR 163 decision 9). A
   state reference that names a unit of another namespace is an edge, but
   the walk shows the other unit only with `unit:read` on it.
5. **Fail closed.** An error from `ListObjects`, an unknown scope kind or an
   empty answer makes an empty scope: the start unit answers 404 (ADR 166
   decision 14), and a denied read appends the entry of a denied request.
6. **The evidence route** needs the read of the state of both units, because
   it shows resource keys and values from both.

## 5. One-way doors

Before the first tag, the schema baseline changes in place (ADR 322), and
the tool-neutral API can break (ADR 73). The doors that stay hard to close:

1. **An inferred edge that blocks a delete.** Once teams depend on a delete
   that a guess blocks, a later change to "list only" reads as a removed
   safety check. This RFC proposes list only (owner question 3).
2. **A value lookup across namespaces.** If one release answers which units
   of another namespace hold a value, the existence of those resources is out,
   and no later release takes it back. This RFC allows it only with a link.
3. **The hash leaves the server.** If a hash is in an API, an export or a
   log, the key cannot change without a break. This RFC keeps the hash
   internal and the tables in the projection class.
4. **The index enters the backup.** A backup with the index holds a second
   copy of state values for as long as the backup lives. This RFC keeps the
   index out of every backup and rebuilds it.
5. **The names of evidence and confidence** in the API, once the console and
   clients filter on them. They are proposed here: `job`, `lock`,
   `declared`, `state` and `identity`, then `exact`, `strong`, `weak`, `hint`.

The row layout, the hash form, the allowlist, the limits and the GIN option
are two-way doors: the index is a projection, and a change is a rebuild.

## 6. Slices

Each slice lands with its e2e step first, through the real CLI.

1. **Measure and benchmark.** A corpus tool reads the state fixtures of the
   e2e suite and the states that a person gives on the command line, and
   prints the produced and consumed values for each resource (only the
   counts). A benchmark with the shapes above goes into `make bench`. No API
   change. The result replaces the estimate of 1.5 consumed values.
2. **State references.** Extraction of `pulumi:pulumi:StackReference`,
   `terraform_remote_state` and `tfe_outputs` in the projection run,
   `graph_unit_refs`, `graph_derived_edges` with the evidence `state`, and
   the unresolved count. **e2e:** the Pulumi CLI runs `pulumi up` on stack B
   with a `StackReference` to stack A. The downstream graph of A lists B with
   `evidence: state` and `confidence: exact`. OpenTofu applies workspace B
   with a `terraform_remote_state` on the `remote` backend of this server.
   The same check applies.
3. **The value index and identity edges.** `identity_produced`,
   `identity_consumed`, `identity_unit_sets`, the diff write, the graph lock
   of the namespace, the evidence route and dismissals. **e2e:** `tofu state
   push` of a fixture state with an `aws_vpc` into workspace A and a fixture
   with an `aws_subnet` whose `vpc_id` is that VPC into workspace B. The
   downstream graph of A lists B with `evidence: identity`, `confidence:
   weak`, `matches: 1` and the example. A dismissal hides it from the walk
   and counts it. `pulumi stack import` of a fixture with an ARN gives
   `strong`.
4. **The scope filter in the store.** The read scope in the edge query of
   both tables, with `hidden` from the store, and the batch check kept.
   **e2e:** a principal with `unit:read` on one collection only walks from a
   unit of it. The answer names no unit of the other collection, and
   `hidden` counts it.
5. **Plan impact.** `POST /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/graph/plan-impact`
   with a stored plan ID. **e2e:** a speculative plan of workspace A that
   removes an output that workspace B reads through `terraform_remote_state`
   lists B with the reason `output`. A fixture state and a plan with a
   replaced `aws_vpc` list the consumer with the reason `value`.
6. **Across linked namespaces.** The `graph.relink` operation and the edge
   row in the namespace of the dependent. **e2e:** two namespaces with a
   link, fixtures pushed into each, the edge shows. After an unlink, the
   next run removes it.
7. **Profiles and the value view.** A profile as the anchor of a blast
   radius (the units that attach it and the profiles that import it), and the
   value view of one value. **e2e:** two units attach one profile. The blast
   radius of the profile lists both.
8. **DNS names** (later), with the confidence `hint`.

## Open questions for the owner

1. **Do walks follow inferred edges by default?**
   ELI5: the server guesses that B uses A's network because B holds A's
   network ID. Should "what breaks if I change A" include B by default, with a
   mark that it is a guess, or only when the person ticks "include guesses"?
   Proposal: follow `strong` and `weak` by default, with the mark. Never
   follow `hint`.
2. **Identity edges across namespaces only with a link?**
   ELI5: two teams in two namespaces use one AWS account. If the server
   matches IDs across them without asking, team 1 can learn what team 2
   built. Should a match across namespaces need the link that an
   administrator makes and both namespaces accept (ADR 216)? Proposal: yes.
3. **Can a guessed edge block a delete?**
   ELI5: today a delete of A stops when the server saw B read A's outputs.
   Should it also stop when the server only guesses that B uses A? Proposal:
   no. Show the guess next to the delete check, and let only exact edges
   block.
4. **Does a person dismiss a wrong guess, and who can?**
   ELI5: sometimes the guess is wrong. A person can hide that one edge with a
   reason, and everyone can see who hid it and why. Proposal: yes, with a new
   action `unit:manage-edges` on the dependent unit, in the roles that can
   change the unit.
5. **Is a profile an anchor of the blast radius?**
   ELI5: 40 stacks use one profile with the cloud credentials. If someone
   changes the profile, all 40 can break. Should "what breaks if I change
   this profile" list them? Proposal: yes, as an anchor, not as edges between
   the 40 units (that would be 780 edges that say nothing).
6. **Is the storage budget right?**
   ELI5: the index costs about 100 bytes for each cloud resource in SQLite
   and about 415 bytes in PostgreSQL, about 2% and 8% of the state that it
   reads. One million resources cost about 100 MB and 415 MB. Is that
   acceptable, or should the first release use the PostgreSQL array layout
   (about 3.7 times smaller on the larger table, but each change rewrites the
   whole list of a unit)? Proposal: rows now, and the array only if slice 1 shows
   more than 4 consumed values for each resource.

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write waits
  for another installation (ADR 248 decisions 3, 6 and 9). The index is a
  projection that each installation builds. A cross-namespace edge is
  written by the dependent namespace under its own fence, from a durable
  operation of that namespace.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
