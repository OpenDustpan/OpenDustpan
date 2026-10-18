# 166. Context and graph: gaps found before the implementation

Status: accepted · 2026-10-18 · supersedes part of ADRs 160 to 165 (see "What this ADR replaces")

## Context

ADRs 160 to 165 decide context on every object, the context read API, the
unit graph, the resource index, cloud sweeps and unmanaged-resource
findings. The plan for the implementation found gaps between these ADRs,
the code on `main`, and two ADRs in review: ADR 154 (history gaps, one 404
rule for reads by ID) and ADR 149 (reads by ID stay in their scope,
decision 4).

Two facts frame this ADR:

- **G1. ADRs 166 to 169 were never written.** The 160s block stops at
  ADR 165. This ADR takes the number 166. No ADR on `main` or in an open
  pull request uses it.
- **G10. The blueprint layer waits for the Catalog code.** ADRs 110 to 117
  design blueprints, but no blueprint object exists in code yet. Until it
  exists, the context chain has no blueprint layer (ADR 160 decision 12),
  and the blueprint routes of context are not in the route table. This ADR
  decides nothing for blueprints beyond their place in the route form of
  decision 13.

The facts on `main` that the decisions build on:

- **Ports with no SQL adapter.** `objectcontext.Store`, `unmanaged.Index`,
  `unmanaged.Links`, `unmanaged.ScanStore`, `unmanaged.FindingStore` and
  `dependency.Store` have reference adapters in memory and conformance
  suites only. So a change to these ports costs no SQL code.
- **Namespace names in the keys.** `objectcontext.Ref` and `graph.Unit`
  hold a `namespace.Name`. `Ref.Key` holds the name of the namespace and
  of a collection. `unmanaged.Links` already holds stable namespace IDs
  (ADR 163 decision 9), but the namespace store does not give these IDs
  yet: `namespace.Namespace` holds a name and no stable ID. Item 1 of the
  open work of ADR 154 adds that ID.
- **Collections have IDs.** The table `collections` has the columns `id`,
  `namespace`, `name_key` and `name`.
- **Durable start keys never expire.** `durable.Store.Start` returns the
  operation that a key already names, "whatever its input, version and
  state". So one fixed start key for each unit starts one operation for
  the life of the unit, and never a second.
- **Each retry is a failure.** `durable.Store.Fail` increments `failures`
  for each attempt that does not end, and the runner ends the operation
  failed after `MaxAttempts` failures. `Release` returns an operation to
  pending with no failure, but with `not_before_ms` at the current time, so
  a claimer can take it again at once. No result says "not yet, try again at a time".
- **A restore fences every namespace.** `backup.Applier.ApplyStaging` and
  `ApplyStagingKeepingAccess` swap the data and increment the failover
  version of every namespace in the apply step. A write that carries a fence
  from before the swap then fails with `namespace.ErrFenced`.
- **The backup has two table lists.** `backup.Tables` lists the tables that
  a backup holds. `backup.ControlTables` lists the tables that a backup
  leaves out and a restore keeps. The conformance suite fails while the
  schema and these lists differ. The portable export reads in one
  `REPEATABLE READ` transaction, so a backup is one consistent snapshot.
- **The new actions have no family.** `identity` declares
  `unit:set-context`, `namespace:set-context`, `collection:set-context`,
  `namespace:read-unmanaged`, `namespace:triage-unmanaged`,
  `namespace:start-sweep`, `namespace:manage-sweeps` and
  `server:link-namespaces`.
  `access.FamilyOf` answers no family for them, and a test holds that
  "until its routes land". So no built-in role holds them.
- **The retention expiry route.** `internal/api/server/deleted.go` serves
  `POST /api/server/deleted-units/sweep`. No tag of OpenDustpan exists.
- **The graph edge port.** `graph.EdgeReader.Edges(units, dir, since,
  limit)` exists. `dependency.Store` reads one unit at a time with
  `Dependents` and `Sources`.

## Decision

### G2. The SQL design of context rows, and backup classes

1. **One table for the entries.** Both dialects get the table
   `object_context`, one row for each entry:

   | Column | Type | Value |
   | --- | --- | --- |
   | `namespace_id` | text | the stable ID of the namespace |
   | `object_kind` | text | `namespace`, `collection`, `unit`, `resource` or `blueprint` |
   | `object_id` | text | empty for a namespace, the collection ID, the unit ID (also for a resource) or the blueprint ID |
   | `tool` | text | the tool of a unit or a resource, else empty |
   | `resource_key` | text | the key of a resource in its state, at most 1 KiB, else empty |
   | `source` | text | `api` or `output` |
   | `fields` | text | the JSON of the five fields and, for a unit output entry, the readme |
   | `warning` | text | the parse error of an output, else empty |
   | `item_id`, `item_seq` | text, bigint | the state item of an output entry, else empty and 0 |
   | `resource_deleted_at_ms` | bigint, nullable | decision 23 |
   | `marks_seq` | bigint | the order of the state item that last set or cleared the mark, else 0 (decision 23) |
   | `updated_at_ms`, `updated_by` | bigint, text | the time and the principal of an API write |

   The primary key is (`namespace_id`, `object_kind`, `object_id`,
   `resource_key`, `source`). A unit ID and a collection ID are stable, so
   a rename of a unit or a collection changes no row. The store resolves
   the names of a `Ref` to IDs in the transaction of each call
   (decision 10).
2. **One table for the owner index.** `object_context_owners` holds one row
   for each owner of each entry: (`namespace_id`, `owner`, `object_kind`,
   `object_id`, `resource_key`, `source`). This is the primary key, so the
   rows of one owner in one namespace are one range of the index.
   `SetAPI`, `DeleteAPI`, `ReplaceOutput` and `DropUnit` write the owner
   rows in the transaction of the entry. A test of each adapter checks that
   the owner rows equal the owners of the stored entries after each write.
3. **`ByOwner` reads the owner index.** It reads one range of
   `object_context_owners` and joins the entries by the primary key. It
   orders by (`object_kind`, `object_id`, `resource_key`, `source`), with
   `api` before `output`. The parameter `after` of `ByOwner` becomes an
   opaque cursor that the store gives with each page, in place of
   `Ref.Key`. `Ref.Key` holds the names of a namespace and a collection,
   which a rename changes. So a page cursor of names could skip or repeat
   rows. The owner text is at most 200 bytes (ADR 160 decision 2), so the
   key fits in a B-tree index of both dialects.
4. **The backup class of context rows: backed up.** `object_context` and
   `object_context_owners` go in `backup.Tables`. The API entries are data
   that people wrote. The server cannot make them again from any other
   source. One table holds both sources, and the output entries are
   small: at most 201 rows for a unit (ADR 160, "Consequences"). The
   export is one snapshot, so the output entries in a backup never come
   from a state item that is newer than the state in the same backup. A
   restore then brings the output entries back with the state.
5. **The backup class of the resource index: rebuildable.** The tables of
   the resource index are a third class, **projection tables**
   (`backup.ProjectionTables`). A backup leaves them out. Both appliers,
   `ApplyStaging` and `ApplyStagingKeepingAccess`, in both dialects, empty
   them in the swap, and the server builds them again (decision 9). The
   conformance suite checks that every table is in exactly one of the
   three lists. The reasons:
   - The index is a pure function of the latest state items. The backup
     holds those items.
   - The index can hold up to 100,000 rows for each unit (ADR 163
     decision 7), which is much more than the state items of the same
     unit hold. A backup of it doubles the data of large units.
   - A rebuild reads each latest state item one time. It is the same read
     as one projection run for each unit.
6. **The projection heads and the readiness rows are projection tables
   too.** `projection_heads` (decision 7) and `resource_index_ready`
   (decision 9) go in `backup.ProjectionTables`. A restore empties them.

### G3. The projection of context and of the resource index

7. **A standalone operation kind, one for each unit, that coalesces.** The
   kind is `projection.unit`, version 1. The table `projection_heads` holds
   one row for each unit: (`namespace_id`, `tool`, `unit_id`, `run_id`,
   `queued`, `started_at_ms`, `marked_run_id`). The start key of a run is
   `projection.unit:<namespace ID>:<tool>:<unit ID>:<run ID>`. The run ID
   is a random ID that the server makes for each start. `started_at_ms` is
   the time of the start. `marked_run_id` is the run ID of the last failed
   run that `MarkIncomplete` recorded, else empty.
   - **The start.** Each transaction that sets a new latest state item of
     a unit also runs one atomic upsert on its head row:

     ```sql
     INSERT INTO projection_heads
       (namespace_id, tool, unit_id, run_id, queued, started_at_ms, marked_run_id)
     VALUES ($1, $2, $3, $4, true, $5, '')
     ON CONFLICT (namespace_id, tool, unit_id) DO UPDATE
       SET run_id = excluded.run_id, queued = true,
           started_at_ms = excluded.started_at_ms
       WHERE NOT projection_heads.queued
          OR NOT EXISTS (SELECT 1 FROM durable_operations o
                         WHERE o.start_key = 'projection.unit:' || $1 || ':' || $2
                                             || ':' || $3 || ':' || projection_heads.run_id
                           AND o.state IN ('pending', 'running'))
     RETURNING run_id
     ```

     `$4` is a new run ID and `$5` is the current time. The upsert takes
     the head when the row does not exist, when `queued` is false, or when
     no operation with the start key of the run ID in the row is pending or
     running. It reads `durable_operations` by the start key in the same
     transaction. When the upsert returns a row, the transaction starts the
     run with that run ID (`durable.StartRequest`). Both dialects support
     this form of upsert. When it returns no row, a live run waits, and the
     transaction starts nothing. Two concurrent transactions on one unit
     serialize on the row, so only one of them takes the head.
   - **Why the start also takes a head with no live operation.** A run
     that ends before its first step, for example with a final failure or
     a cancel, leaves `queued` true. Without the `NOT EXISTS` check, no
     later item could start a run for that unit. The check also takes a
     head whose operation is missing, so a lost start cannot block the
     unit.
   - **The reconcile tick.** A durable schedule `projection.reconcile`
     runs every 10 minutes. It starts runs only for heads that stay in
     `queued`: the head rows with `queued` true and no pending or running
     operation for their run ID. It applies the limit of 1,000 heads for
     each tick after this filter, oldest `started_at_ms` first. For each
     head, it takes the head and starts a run with the same upsert. So a
     unit whose run ended before its first step gets its last item
     projected with no new item. The conformance suite holds the case "a
     run that fails before its first step does not block the next start".
   - **Why a random run ID.** A durable start key names one operation for
     ever. A restore empties `projection_heads` and keeps the durable
     operations (decision 9). A counter in the head row would then start
     again at 1, and a new start would get an old operation that ended. A
     random run ID never names an old operation. The writes
     that set a new latest state item are: the end of a Pulumi update,
     a Pulumi stack import, an OpenTofu state version that becomes the
     current one, and a forced state write of either tool.
   - **The run.** The first step of a run sets `queued` to false when the
     head row holds its run ID, and records the step, in one transaction.
     After that transaction commits, the run reads the latest state item
     in a new transaction, with secret values masked (ADR 41). Then it
     writes the output entries of context (`objectcontext.Store.ReplaceOutput`),
     the index entries (`unmanaged.Index.ReplaceUnit`) and the deleted
     marks of decision 23, each with the fence of the namespace and with
     the order of the item. The run reads the fence when it reads the
     item. For a soft-deleted unit, it writes the index entries with the
     deleted mark (`Index.SetDeleted`), so a rebuild keeps the findings of
     the kind left behind.
   - **A unit with more than 100,000 resources.** The run indexes the
     first 100,000 resources by resource key (ADR 163 decision 7), and the
     index write records the mark "not fully indexed" of the unit. The run
     calls `MarkResources` with `complete` false and the keys of those
     first 100,000 resources, so the method sets no deleted mark (decision
     23). A legal unit never gets `ErrTooManyKeys`.
   - **A restore during a run.** The apply step of a restore fences every
     namespace. So a run that read its item before the swap fails its
     writes with `namespace.ErrFenced` and writes nothing from the old
     data. The rebuild of decision 9 then projects the restored items.
   - **Why this coalesces and loses no item.** Items that arrive before the
     first step find `queued` true, and the run reads them all, because it
     reads the latest item after that step. An item that arrives after the
     first step sets `queued` again and starts one more run. So at most one
     run waits for each unit, and the last item always gets a run. Each
     write keeps the entries of the highest order, so a late run or a run
     again does no harm.
   - **A unit that no longer exists.** A run that finds no live or
     soft-deleted unit with its ID ends with success and writes nothing.
   - **Failure.** A run that fails retries with the backoff of ADR 72. A
     run that fails for the last time ends failed. When it fails before its
     first step, `queued` stays true, and the next state item or the
     reconcile tick starts a new run. When it fails after its first step,
     `queued` is false, and only the next state item of the unit starts a
     new run. The reconcile does not start it again, so a unit whose
     projection always fails does not retry in a loop. The mark "not fully
     indexed" records the failure.
   - **"Not fully indexed" is a store write by the reconcile.** The
     reconcile tick also reads the head rows whose operation ended failed
     and whose `marked_run_id` is not their `run_id`, with the same limit
     of 1,000 after this filter, oldest `started_at_ms` first. For each
     one, it writes the mark "not fully indexed" of the unit (ADR 163
     decision 7) with `unmanaged.Index.MarkIncomplete(unit, runID, fence)`.
     In its own transaction, `MarkIncomplete` checks that the head row of
     the unit still holds `runID` and that the operation of that run ended
     failed. Only then does it write the mark and set `marked_run_id` to
     `runID`. Else it changes nothing and returns nil. So a mark never
     covers a newer run. The failed run itself writes no mark, because its
     last attempt can end before any of its writes.
   - **A success clears the failure mark.** The next run that succeeds for
     the unit clears the failure mark in the transaction of its index
     write. A success for a unit with more than 100,000 resources keeps
     the mark of ADR 163 decision 7.
   - **A projection inside a rebuild.** A rebuild (decision 9) runs the
     same projection of one unit in its own step, but it does not read or
     write `projection_heads`, and it uses the fence of the rebuild. So a
     rebuild and a live run of one unit can run at the same time. Each
     write keeps the entries of the highest order, so the two give the
     same result.
8. **Independent of blame.** The run does not wait for the blame operation
   of ADR 150, which is not built yet, and does not share its operation.
   The reasons:
   - The context read API and the resource index can ship before blame.
   - A blame run reads every state item in order. A projection run reads
     only the latest item, and coalesces. The two have different start
     keys and different costs.
   - A failure of one does not stop the other.

   The cost is one more masked read of the latest item for each projection
   run. With coalescing, a unit that writes ten state items in a minute
   costs one or two reads, not ten. When blame lands, the two can share a
   read through a cache of the masked item. They stay two operations.
9. **The rebuild after a restore.** The apply transaction of a restore
   (`ApplyStaging` and `ApplyStagingKeepingAccess`, in both dialects)
   empties the projection tables in the swap, and in the same transaction
   starts one operation `projection.rebuild`, version 1, for each
   namespace that the backup holds, with the start key
   `projection.rebuild:<namespace ID>:<restore ID>`. The restore ID is in
   the key, so a second restore of the same backup starts new rebuilds. A
   crash after the swap cannot leave a namespace with no row and no
   rebuild.
   - **The port change.** `backup.Applier.ApplyStaging` and
     `backup.AccessKeepingApplier.ApplyStagingKeepingAccess` take a
     `[]durable.StartRequest`. The restore service builds the list, one
     start for each namespace ID of the staged data, so the backup
     adapters know no projection kind. The adapters start the operations
     in the swap transaction, after the copy of `durable_operations`, in
     both dialects. A start before the copy would go with the replaced
     control rows.
   - **The steps.** A rebuild reads the fence of its namespace one time,
     in its first step, and uses that fence for every write, also for the
     write of the ready row. It reads the live and soft-deleted units of
     its namespace in pages of 100, by unit ID, and records a step for
     each page. So a takeover continues from the last page. For each unit,
     it runs the projection of decision 7 in its own step. At the end it
     writes the row of the namespace in `resource_index_ready`, in one
     transaction with the last step.
   - **A restore during a rebuild.** `namespace.ErrFenced` is a permanent
     failure for a rebuild: the rebuild ends failed with no retry. The
     restore that fenced it started its own rebuild of the namespace. So
     the old rebuild writes no row in `resource_index_ready` from the old
     data. The conformance suite holds the case "restore during rebuild:
     the old rebuild writes no ready row".
   - **A unit that fails in a rebuild.** A unit projection that fails 3
     times in a row in its step, with an error that is not
     `namespace.ErrFenced`, fails permanently. The step then writes the
     mark "not fully indexed" of the unit with
     `unmanaged.Index.MarkIncompleteFromRebuild(unit, seq, fence)`, records
     itself as done, and the rebuild goes on with the next unit. The method
     writes the mark only when the index of the unit holds no write of the
     order `seq` or higher, so a live run that succeeded keeps its result.
     The rebuild has no head row, so `MarkIncomplete` does not apply. So
     "ready" means that the rebuild tried every unit, not that every unit
     succeeded. The next state item of a failed unit starts a live run.
   - **Fail closed while the index is not ready.** A namespace with no row
     in `resource_index_ready` has an index that is not complete. While it
     is not complete, the state check and the match of a cloud sweep in
     that namespace do not run. Their durable operation ends the attempt
     with the result "not yet, try at T", with T 1 minute later, and the
     sweep keeps its status. They resolve no finding and open no finding.
     A link (ADR 163 decision 9) to a namespace that is not ready also
     makes the linked namespace wait. Else an empty index would resolve a
     managed-twice finding, and classify a key as not in any unit.
   - **A needed change of the durable package: "not yet" is not a
     failure.** A failed attempt counts toward `MaxAttempts`, so a long
     rebuild would make the state check and the match fail for good. The
     durable package gets a step result `durable.NotYet(at time.Time)` and
     a store method `Defer(ctx, c Claim, notBefore time.Time) error`.
     `Defer` returns the operation to pending with `not_before_ms` at
     `notBefore`, clears the claim, and keeps `attempt` and `failures`.
     It returns `ErrClaimLost` when `c` is not the live claim, and ends
     the operation canceled when a person asks for a cancel, as `Release`
     does. The runner calls `Defer` for a step that returns `NotYet`. The
     `durabletest` suite gets the cases: a deferred operation is not
     claimable before `notBefore`, is claimable after it, and keeps its
     failure count. This change lands before the state check and the
     match.
   - **The reconcile.** At each server start, and on the schedule
     `projection.reconcile` of decision 7, the server reads the namespaces
     with no row in `resource_index_ready`. For each one with no live
     (pending or running) `projection.rebuild`, it starts a rebuild with
     the start key `projection.rebuild:<namespace ID>:<random ID>`. So a
     rebuild that failed for the last time, or a namespace that a lost
     write left with no row, gets a new rebuild. The reconcile reads and
     starts in one transaction that locks the row of the namespace, so two
     servers that start at the same time start one rebuild, not two.
   - **The lookup of a live rebuild.** The reconcile does not search
     `durable_operations` by a prefix of `start_key`. `durable.StartRequest`
     gets a field `Subject`, a tool-neutral text that names the object of
     the operation, and `durable_operations` gets the column `subject`
     with the index (`kind`, `subject`) on the pending and running rows. A
     rebuild sets `Subject` to the namespace ID. The reconcile reads the
     live rebuild of a namespace with one range read of this index.
   - **The installation signal.** The restore status
     (`GET /api/server/restores/{id}`) answers `namespacesNotReady`, the
     number of namespaces with no row in `resource_index_ready`, and
     `rebuildsFailed`, the number of `projection.rebuild` operations that
     ended failed for such a namespace. The metrics give the same two
     numbers as gauges. The signal holds no namespace name, ID or other
     namespace data, so the actions of the installation suffice to read
     it. While a namespace is not ready, a person can cancel a waiting
     sweep, and the cancel works. A waiting sweep keeps its target busy,
     so a new sweep of that target answers `unmanaged.ErrScanBusy` until
     the waiting sweep ends or a person cancels it.
   - **The API shows "index not ready".** The routes of findings, sweeps
     and the inventory answer the field `indexReady: false` for a
     namespace with no row in `resource_index_ready`, and the console shows
     "Index not ready". The start of a sweep or a state check in such a
     namespace succeeds, and the sweep waits as above.
   - **A new namespace.** The create of a namespace writes its row in
     `resource_index_ready` in its transaction, because an empty namespace
     has a complete index.
   - **The first start of a server with these tables.** The migration that
     makes `resource_index_ready` writes no row. The reconcile at the
     first server start after it starts one `projection.rebuild` for each
     namespace.
   - **Output entries.** The rebuild also writes the output entries of
     context. A backup holds them, so this changes a row only when a run
     was pending at the time of the backup. The durable operations are
     control tables (ADR 72), so a pending run of the live server is not
     in the backup.

### G4. Keys by the stable namespace ID

10. **The stores key by the namespace ID.** Every row of the context store,
    of the resource index, of the projection heads and of the dependency
    edges holds the stable ID of the namespace (ADR 163), never its name.
    The ports keep `namespace.Name` in `objectcontext.Ref` and in
    `graph.Unit`, because a caller names a namespace by its name. Each
    store resolves the name to the ID of the namespace that has the name
    now, in the transaction of each call. ADR 154 decision 2 and
    `unmanaged.Links` use the same rule. A collection reference resolves
    its name to the collection ID in the same way.
11. **A purge of a namespace deletes its context and its index.** The purge
    of a namespace deletes every row of its namespace ID in
    `object_context`, `object_context_owners`, the resource index,
    `projection_heads` and `resource_index_ready`, in its steps, with the
    fence of the namespace. The findings, the sweep targets and the ignore
    rules go with the namespace data. A soft delete of a namespace keeps
    all of them, so a restore in its recovery window brings them back.
12. **Nothing carries over to a namespace that a team creates again.** A
    namespace created under the name of a purged namespace has a new ID.
    It reads no context entry, no index entry, no edge and no link of the
    purged namespace. Unit IDs and collection IDs are new too (ADR 19). So
    a reference that a client kept from before the purge names an object
    that does not exist, and the read answers 404 (decision 14). The
    `objectcontexttest`, `unmanagedtest` and `dependencytest` suites each
    get the case "purge, then a new namespace with the same name sees no
    row". The daily offset of the state check (ADR 163 decision 11)
    takes a hash of the namespace ID, not of the name, so a new namespace
    does not inherit the time of the purged one.

### G5. The namespace in the path, and one 404 rule

13. **The routes of context.** These replace the paths of ADR 160
    decision 18 and ADR 161 decision 1:
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}`
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}/collections/{collection}`
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}/units/{tool}/{unit_id}`
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}/units/{tool}/{unit_id}/resource?key=...`
    - `GET`, `PATCH` and `DELETE /api/context/namespaces/{namespace}/blueprints/{blueprint}`
      (after the Catalog code, G10)
    - `GET /api/context/namespaces/{namespace}/owners?owner=...&after=...`

    The routes of the graph replace those of ADR 162 decision 15:
    - `GET /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/graph/downstream?hops=&maxNodes=&window=`
    - `GET /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/graph/upstream?hops=&maxNodes=&window=`
    - `GET /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/graph/focus?neighbors=&window=`
    - `GET /api/inventory/namespaces/{namespace}/graph/path?from={tool}/{unit_id}&to={namespace}/{tool}/{unit_id}&maxHops=&undirected=`

    The `from` unit of a path is in the namespace of the path. The `to`
    unit names its namespace, because an edge can link two namespaces
    (ADR 162 decision 13).
14. **One 404 rule.** The rule of ADR 154 decision 9 and ADR 149
    decision 4 applies to each route of decision 13:
    - **The namespace first.** The handler finds the namespace of the
      path, live or soft-deleted. An unknown namespace answers 404. Then it
      checks `namespace:read` on that namespace. When the caller does not
      hold it, the handler answers the same 404.
    - **Then the object.** The handler finds the object by its ID or name
      only in the namespace that the route names for it: the namespace of
      the path, or for the `to` unit of a path, the namespace in `to`. It
      finds the live object or the soft-deleted object. When it finds none,
      it answers 404.
    - Then it checks the read action of the object (ADR 160 decision 16).
      When the caller does not hold it, the handler answers the same 404.
      So a read by ID needs `namespace:read` on the namespace of the path
      and the read action of the object, as ADR 154 decision 9 says.
    - So an unknown namespace, an unknown object, an object of another
      namespace, a purged object and an object that the caller cannot read
      all answer the same 404, with the same body. The answer never tells
      that the object exists.
    - **A resource that the state does not hold.** The `PATCH` of the
      context of a resource checks the key against the index entries of
      the unit, not against the state item. A key that the index does not
      hold, with no mark of decision 23, answers 404. A person can add
      context only to a resource that exists.
    - **The index lags a new item.** When the order of the index of the
      unit is lower than the order of its latest state item, a projection
      run waits. Then a key that the index does not hold answers 409
      (`objectcontext.ErrIndexBehind`), and the message tells the client to
      try again after the projection. So a key from an item that is not
      projected yet never answers 404.
    - **A unit that is not fully indexed.** For a unit with the mark "not
      fully indexed", a key that the index does not hold answers 409
      (`objectcontext.ErrResourcesUnreadable`), because the server cannot
      check the key.
    - **Client-encrypted state.** The server cannot read the resources of a
      unit with client-encrypted state (ADR 163 decision 6). So a `PATCH`
      of the context of a resource of such a unit answers 409
      (`objectcontext.ErrResourcesUnreadable`). The context of the unit
      itself still works.
    - For a path, the handler applies the rule to the `from` unit and to the
      `to` unit, and answers the same 404 for either.
    - For a `PATCH` or a `DELETE`, the handler checks the read action
      first. A caller with the read action and without the set action gets
      403, because that caller can already see the object.
    - The stores take the namespace of the route.
      `objectcontext.Store.Entries` and `ByOwner` never answer an entry of
      another namespace, and the suite holds the case.
15. **A soft-deleted unit.** The `GET` routes answer the context of a
    soft-deleted unit with the mark `deleted`, because its entries stay
    until the purge (ADR 160 decision 15). A `PATCH` of the context of a
    soft-deleted unit or of its resources answers 409
    (`objectcontext.ErrUnitDeleted`). The API accepts a `DELETE`, so a person can
    clear an API value before a restore.

### G6. The batch read of dependency edges

16. **`dependency.Store.Edges`.** The port gets this method, which ADR 162
    asked for:

    ```go
    // Edges returns the unit edges that touch units in dir: for Downstream
    // the edges whose source is in units, for Upstream the edges whose
    // dependent unit is in units. It returns only edges that name a
    // dependent unit, and whose last time seen is at or after since. A zero
    // since reads every edge. The order is the last time seen, newest
    // first, then the source, the dependent, the kind and the evidence. It
    // returns at most limit+1 edges, so the caller sees that more exist. An
    // edge whose two units are both in units comes one time. An edge whose
    // other unit is in another namespace is part of the answer. A
    // soft-deleted unit keeps its edges. units holds 1 to MaxEdgeUnits
    // units, and limit is 1 to MaxEdgeLimit. It returns ErrInvalidQuery for
    // an empty or too large batch, an invalid unit, a limit out of range or
    // an unknown direction.
    Edges(ctx context.Context, units []history.Unit, dir Direction, since time.Time, limit int) ([]Edge, error)
    ```

    - `dependency.Direction` has the values `Downstream` and `Upstream`.
      The graph adapter maps `graph.Direction` to it.
    - `MaxEdgeUnits` is 500 and `MaxEdgeLimit` is 5,000: the limits "units
      in one call to a port" and "edges read in one hop" of ADR 162
      decision 9. `MaxEdgeUnits` equals `graph.MaxBatch` (500), so the
      graph adapter never splits a batch. A test of the adapter holds the
      two values equal.
    - `ErrInvalidQuery` is a new sentinel of `dependency`. It wraps the
      error of `history.Unit.Validate` for an invalid unit. The graph
      adapter translates `dependency.ErrInvalidQuery` into
      `graph.ErrInvalidQuery`.
    - **A purged dependent in another namespace.** An edge to a dependent
      in another namespace stays until the window drops it, also after a
      purge of that dependent. The graph shows such a dependent as an
      unknown unit, because the access filter and the read of the unit find
      no unit.
    - The order is newest first, as `Dependents` and `Sources`. So a cut at
      the limit keeps the newest edges, as the focus view needs (ADR 162
      decision 7). The ties have a full order, so two calls answer the same
      page. `graph.EdgeReader.Edges` takes the same order.
    - Edges that name only a principal are not in the answer: they are not
      graph edges (ADR 162 decision 1).
    - The access filter is not in the store. The graph applies
      `graph.Access` to each unit that an edge names (ADR 162 decision 10).
17. **The SQL indexes.** The edge table has one index on the source unit
    and one on the dependent unit, each with the last time seen
    descending. One call is then one range read for each unit of the batch.
18. **The conformance cases.** `dependencytest` gets these cases: each
    direction, the window, the limit and limit+1, an edge with both units
    in the batch, an edge across two namespaces, a principal edge left
    out, a soft-deleted unit, a batch of 0 and of 501 units, a limit of 0
    and of 5,001, and an unknown direction. `graphtest.RunEdgeReader` then
    runs over the adapter that calls `Edges`, and the adapter has no loop
    over `Dependents` and `Sources`.

### G7. The built-in roles of the new actions

19. **The role-to-action table.** Eight actions get a family and built-in
    roles. The seven namespace actions join the data family
    (`access.FamilyData`), because each one acts inside a namespace.
    `server:link-namespaces` joins the administration family
    (`access.FamilyAdministration`), because its object is the
    installation and it joins the data of two namespaces. The built-in
    roles get them as follows:

    | Action | Family | Built-in roles that hold it |
    | --- | --- | --- |
    | `unit:set-context` | data | `writer`, `maintainer`, `admin`, `account-administrator`, `site-administrator` |
    | `collection:set-context` | data | `maintainer`, `admin`, `account-administrator`, `site-administrator` |
    | `namespace:set-context` | data | `admin`, `account-administrator`, `site-administrator` |
    | `namespace:read-unmanaged` | data | `auditor`, `writer`, `maintainer`, `admin`, `account-administrator`, `site-administrator` |
    | `namespace:triage-unmanaged` | data | `maintainer`, `admin`, `account-administrator`, `site-administrator` |
    | `namespace:start-sweep` | data | `maintainer`, `admin`, `account-administrator`, `site-administrator` |
    | `namespace:manage-sweeps` | data | `admin`, `account-administrator`, `site-administrator` |
    | `server:link-namespaces` | administration | `account-administrator`, `site-administrator` |

    `viewer`, `secret-viewer`, `installation-operator` and `token-manager`
    hold none of the eight.

    The reasons, with least privilege:
    - **Viewers never write.** A viewer and a secret viewer get no set
      action, no triage action and no sweep action.
    - **`unit:set-context` goes with `unit:write`.** A writer can already
      change the reserved output of a unit with a state write. The API
      gives no more than that (ADR 160 decision 16).
    - **`collection:set-context` goes with the roles that manage
      collections.** The maintainer creates, changes and deletes
      collections. A writer does not.
    - **`namespace:set-context` is for the admin only.** The owners and the
      on-call of a namespace are the default of every unit in it
      (ADR 160 decision 11). A wrong value reaches each unit with no owners
      of its own.
    - **`namespace:read-unmanaged` is not for a viewer.** A finding of not
      in any unit names a cloud resource that no unit holds, with its tags,
      and a sweep target holds its command. A viewer reads units, not the
      cloud account. A viewer still gets the counts of open findings on a
      unit (ADR 165 decision 15). The writer and the maintainer change
      units, and must see a managed-twice finding before a change. The
      auditor reviews.
    - **`namespace:triage-unmanaged` is for the maintainer and the admin.**
      An ignore and an ignore rule hide findings for every person of the
      namespace. That is a decision for the namespace, as the delete and
      the restore of units are for the maintainer.
    - **`namespace:start-sweep` is for the maintainer and the admin.** A
      start runs a command that a target already holds, and a state check
      reads only the state of the namespace. The maintainer operates the
      units of a namespace, triages its findings, and must see the result
      of a change soon. A writer does not triage, so a writer does not
      start a sweep.
    - **`namespace:manage-sweeps` is for the admin only.** The command of a
      target decides what a runner starts, and its scopes decide what a
      sweep can hide (ADR 164 decisions 17 and 19). So only the roles that
      administer a namespace hold it.
    - **`server:link-namespaces` is for the installation administrators
      only.** A link lets the classification of one namespace read the
      units of another (ADR 163). Only the roles that administer the
      installation hold it. The installation operator manages no
      namespace, so it does not hold it.
    - **No operator data.** The installation operator and the token
      manager read no namespace data, so they get none of the eight.

    These descriptions of built-in roles in `access` change:
    - `auditor`: "Reads like a viewer, and reads the audit log, the access
      review and the unmanaged resources."
    - `writer`: "Reads, runs updates, plans and applies, sets the context
      of units, and reads the unmanaged resources."
    - `maintainer`: "Writes, and makes, renames, deletes, restores, unlocks
      and moves units, manages collections and their context, triages
      unmanaged resources and starts sweeps."
    - `account-administrator`: "Manages principals, groups, roles, grants,
      namespaces and the links between namespaces, makes the tokens of
      every service principal, and holds every data action."

    The descriptions of `viewer`, `secret-viewer`, `admin`,
    `installation-operator`, `site-administrator` and `token-manager` do
    not change. The doc comments of the role constants change with the
    descriptions.

    A custom role can hold any of the eight. The family, the role actions
    and the change of the test "no family until its routes land" land in
    the change that adds the first route of each action.

### G8. The route of the retention expiry

20. **`/sweep` becomes `/expire`.** The route
    `POST /api/server/deleted-units/sweep` becomes
    `POST /api/server/deleted-units/expire`, as ADR 163 asks ("Requests to
    other designs"). Its action, its body and its answers do not change.
21. **The old path goes before the first tag.** No tag shipped the old
    path (ADR 73). So the change deletes it and adds no alias and no
    redirect. The same change updates the guide of deleted units, the
    e2e suite, the coverage list of the e2e suite and the entry
    "Retention expiry" in `GLOSSARY.md`. The console change merges before
    the next server tag.
22. **The Go names follow.** The handler, the path constant and the
    response type of the route take the word "expire". The Go name
    `inventory.Sweeper` is older than the rule of one word for each
    concept, and its rename is not part of this decision.

### G9. Context on a deleted resource

23. **Keep it, and mark it deleted.** This answers ADR 160 open question 4.
    Each projection run (decision 7) compares the resources of the latest
    state item with the resource entries of context of the unit, of both
    sources:
    - An entry whose resource the latest item does not hold gets
      `resource_deleted_at_ms`, the time of the run, when it has none.
    - An entry whose resource the latest item holds loses its mark. So a
      resource that comes back, for example after a rollback, keeps its
      context.
    - A unit with client-encrypted state gets no marks, because the server
      cannot read its resources (ADR 163 decision 6). The rule fails
      closed: it never deletes context on a guess.
    - A soft-deleted unit gets no marks. The rules of the unit apply
      (ADR 160 decision 15).
    - **The higher order wins.** Each entry stores `marks_seq`, the order
      of the state item that last set or cleared its mark. A run changes
      the mark of an entry only when the order of its item is higher than
      `marks_seq`. So a late run of an older item, or a rebuild that runs
      next to a live run, never sets or clears a mark against a newer item.
    - **The store method.** The run calls
      `objectcontext.Store.MarkResources(ctx, unit, seq, presentKeys, complete, now, fence)`.
      `presentKeys` holds the resource keys of the item, at most 100,000
      (the limit of ADR 163 decision 7). `complete` is false when the item
      holds more resources than `presentKeys`. In one transaction, the
      method clears the mark of each entry whose key is in `presentKeys`.
      When `complete` is true, it also sets the mark at `now` on each
      resource entry of `unit` whose key is not in `presentKeys` and that
      has no mark. When `complete` is false, it sets no mark, because a key
      outside `presentKeys` can still be in the state. It changes an entry
      only when `seq` is higher than `marks_seq`, and sets `marks_seq` to
      `seq` on each entry it changes. It returns `ErrInvalidObject` for an
      invalid unit, the new sentinel `ErrTooManyKeys` for more than 100,000
      keys, `namespace.ErrFenced` for a stale fence, and nil when it
      changes no row. The run never passes more than 100,000 keys (decision
      7), so a legal unit never gets `ErrTooManyKeys`.
24. **The read API shows the mark.** The `context` section of a resource
    answers `deleted: true` and `deletedAt`. `ByOwner` answers a marked
    entry with the same mark, so the console can show it apart. A `PATCH`
    of a marked resource answers 409 (`objectcontext.ErrResourceDeleted`),
    so a person does not add context to a resource that the state does
    not hold. The API accepts a `DELETE`.
25. **Retention.** A marked entry stays for the soft-delete retention
    period of the installation (ADR 47, `inventory.DeletedRetention` by
    default), the same window as a deleted unit. Then the retention expiry
    deletes it with
    `objectcontext.Store.ExpireMarked(ctx, before, fence, e)`. The method
    deletes, in one namespace and one transaction, at most 1,000 marked
    entries whose mark is older than `before`, with their owner rows. It
    returns the number it deleted, so the expiry calls it again while the
    number is 1,000. For each unit with deleted API entries, it appends one
    audit entry `e` with the resource keys and the fields it cleared,
    because people wrote those values. An output entry needs no audit
    entry: the next output can bring it back. It returns
    `namespace.ErrFenced` for a stale fence. The suite of
    `objectcontext.Store` gets the cases: the mark, the clear of the mark,
    an older `seq` that changes no mark after a newer one, the 409 of a
    `PATCH`, the limit of 1,000, and the delete at the end of the
    retention period.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 160, decision 8:** "The operation runs with the operation that
  computes blame for the same item (ADR 150)". The projection is now a
  standalone operation, `projection.unit` (decisions 7 and 8).
- **ADR 160, decision 13:** "The SQL adapters and their migrations come
  later." Decisions 1 to 6 give the tables and their backup classes.
  `ByOwner` takes an opaque cursor in place of `Ref.Key` (decision 3).
- **ADR 160, decision 16, in part:** "The built-in roles that write a unit
  get `unit:set-context`. The roles that administer a collection or a
  namespace get the action of that object." Decision 19 gives the full
  table.
- **ADR 160, decision 18, and ADR 161, decision 1:** the paths of the
  context routes now have the namespace in the path (decision 13).
- **ADR 160, "Requests to other designs", first item:** one masked read
  does not serve blame, context and the index together (decision 8).
- **ADR 160, open question 4:** context on a deleted resource stays with a
  mark, for the retention period (decisions 23 to 25).
- **ADR 161, decision 3, in part:** the 404 also covers an object of
  another namespace (decision 14).
- **ADR 162, decisions 11 and 15:** the routes have the namespace in the
  path, and the 404 rule covers the `to` unit of a path (decisions 13 and
  14). `graph.EdgeReader.Edges` answers newest first (decision 16).
- **ADR 162, "Requests to other designs", first item:** the batch method
  is `dependency.Store.Edges` (decisions 16 to 18).
- **ADR 163, decision 5, in part:** "one start key for each unit". The
  start key now holds a random run ID, and the head row of the unit gives
  the coalescing, because a durable start key names one operation for ever
  (decision 7).
- **ADR 163, the `unmanaged.Index` port:** the port gets
  `MarkIncomplete(unit, runID, fence)` and
  `MarkIncompleteFromRebuild(unit, seq, fence)`, which write the mark "not
  fully indexed" after a failed projection (decisions 7 and 9). A success
  over the limit of 100,000 entries keeps the mark of ADR 163 decision 7.
- **ADR 163, decision 11, in part:** the daily offset hashes the namespace
  ID (decision 12).
- **ADR 163, "Requests to other designs", first item:** the route is
  renamed, and the old path goes with no alias (decisions 20 to 22).
- **ADR 164, decision 19, and ADR 165, decision 15:** the built-in roles
  of `namespace:read-unmanaged`, `namespace:triage-unmanaged`,
  `namespace:start-sweep` and `namespace:manage-sweeps` (decision 19).
  ADR 164 decisions 14 and 15: the match waits with the result "not yet"
  while the index of its namespace is not ready (decision 9).
- **ADR 163, "Consequences", the item "Identity":** the family and the
  built-in roles of `server:link-namespaces` (decision 19).
- **ADR 72, in part:** the durable package gets the step result `NotYet`
  and the store method `Defer`, which are not failures, and the field
  `StartRequest.Subject` with its indexed column (decision 9).
- **The backup ports:** `backup.Applier.ApplyStaging` and
  `backup.AccessKeepingApplier.ApplyStagingKeepingAccess` take a
  `[]durable.StartRequest` and start the rebuilds in the swap (decision 9).

## Consequences

- The context store and the resource index get SQL adapters with known
  tables, indexes and backup classes.
- A backup holds every value that a person wrote. It does not hold the
  resource index, which can be large. After a restore, findings do not
  change until the index of each namespace is complete again.
- A unit that writes many state items costs at most one waiting
  projection run. The projection ships before blame. A run that ends
  before its first step does not block the unit: the next start or the
  reconcile tick takes the head.
- A namespace whose index is not ready shows "Index not ready", and its
  state check and match wait without a failure.
- A namespace created again under an old name starts empty, with no
  context, index, edges or links.
- Each read by ID of context and of the graph answers one 404 for
  "unknown", "other namespace", "purged" and "forbidden".
- The graph reads the edges of up to 500 units in one store call.
- `GLOSSARY.md` gets the terms projection run and projection table.
- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. The stable namespace ID (ADR 154, open work item 1).
  2. `dependency.Store.Edges` with its cases, then the graph adapter.
  3. The context tables, the owner index and `ByOwner` with its cursor,
     with `objectcontexttest` cases for decisions 3, 12, 14 and 25. The new
     sentinels of `objectcontext`: `ErrResourcesUnreadable`,
     `ErrUnitDeleted`, `ErrResourceDeleted`, `ErrIndexBehind` and
     `ErrTooManyKeys`.
  4. `durable.NotYet`, `Store.Defer` and `StartRequest.Subject` with their
     `durabletest` cases.
  5. `backup.ProjectionTables`, the resource index tables, the
     projection heads, `projection.unit`, `projection.rebuild` and
     `projection.reconcile`. With them: `unmanaged.Index.MarkIncomplete`
     and `MarkIncompleteFromRebuild` with their `unmanagedtest` cases, the
     new `[]durable.StartRequest` parameter of `ApplyStaging` and
     `ApplyStagingKeepingAccess` with the starts after the copy of
     `durable_operations` in both dialects, and the installation signal
     of decision 9.
  6. The doc comment of `unmanaged.Index` and the entry "Resource index"
     in `GLOSSARY.md` get the limit of 100,000 entries with the mark "not
     fully indexed", the failure mark, and the rebuild after a restore.
  7. The routes of decision 13, with the families and the roles of
     decision 19.
  8. The rename of decisions 20 to 22, with the console change.
