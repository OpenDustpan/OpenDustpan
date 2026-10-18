# 154. History: seven gaps found before the implementation

Status: accepted · 2026-10-18 · supersedes part of ADRs 150 and 151 (see "What this ADR replaces")

## Context

ADRs 150 to 153 decide the history model: blame, the point-in-time views,
item keys, redaction, retention and safe delete. The plan for the
implementation found seven gaps between these ADRs and the code on `main`,
and between these ADRs and three ADRs in review: ADR 128 (agents in the job
model), ADR 149 (reads by ID stay in their scope) and ADR 198 (change order
history).

The facts on `main` that the decisions build on:

- **No adapter exists yet** for `secret.ItemKeyIndex`, `secret.ItemSealer`
  or `inventory.HistoryReader`. Only the ports, `secrettest.RunItemKeys`,
  the `historytest` suites with an in-memory store, and `inventory.ReplayAt`
  exist. So a change to these ports costs no adapter code.
- **`secret.ItemSealer.NewItemKey(ctx, ref, fence)`** writes the key blob
  and then the key record, in a transaction of its own. The create ports of
  the tools (`stack.UpdateStore.CreateUpdate`,
  `workspace.StateVersionStore.CreateStateVersion`) take no item key.
- **`secret.ItemKind.Valid`** accepts only `state`, `plan` and `job-log`.
  `history.ItemRef` names a unit (`history.Unit`) and nothing else.
- **`inventory.Change.Seq`** says that the sequence "increases in commit
  order across the installation". `inventory.HistoryReader.ListChanges` pages
  by `ChangeCursor{Seq}` in one namespace. `inventory.ReplayAt` takes, for
  each unit, the row with the highest sequence.
- **`history.RedactionLog.GetRedaction(ctx, ns, id)`** takes the namespace.
- **`stack.StateAtReader.StateAt` and `workspace.StateAtReader.StateAt`**
  take the stable ID of the unit, and answer a soft-deleted unit "as before
  its delete". No route reaches them by ID.
- **The PostgreSQL adapter** checks the fence with the namespace row
  `FOR SHARE` for most writes, and `FOR UPDATE` for audited writes, so that
  audit appends form one sequence for each namespace
  (`inFencedUpdateTx`). The SQLite adapter runs each write transaction as
  `BEGIN IMMEDIATE` (`_txlock=immediate`), so its writes run one at a time.
- **The inventory API** names a deleted unit by tool and unit ID, with the
  namespace in a query value (`/api/inventory/deleted-units/{tool}/{unitID}`),
  and names collections under `/api/inventory/namespaces/{namespace}/`.

## Decision

### G1. The sequence of the inventory change log

1. **One counter row for each namespace.** The store keeps one row for each
   namespace in the table `inventory_change_heads` (namespace ID, last
   sequence). Each transaction that appends a change log row takes the next
   sequence with one statement on that row, in the same fenced transaction
   as the inventory write:

   ```sql
   INSERT INTO inventory_change_heads (namespace_id, last_seq) VALUES ($1, 1)
   ON CONFLICT (namespace_id) DO UPDATE SET last_seq = inventory_change_heads.last_seq + 1
   RETURNING last_seq;
   ```

   The row lock lasts until the commit. So a second transaction of the same
   namespace waits, and gets the next number only after the first commits.
   The order of the sequences is the order of the commits in the namespace.
   A transaction that rolls back also rolls back its number.
2. **The sequence is per namespace, not per installation.** This replaces
   the words "across the installation" of the `inventory.Change` doc.
   `ListChanges`, `UnitsAt` and `ReplayAt` read one namespace, so no reader
   needs an order across namespaces. The primary key of a change log row is
   (namespace ID, sequence). The change log rows, the counter row and the
   rows of the redaction log hold the stable ID of the namespace, never its
   name, as the links of ADR 163 do. A reader names the namespace by its
   name, and the store resolves the name to the ID of the namespace that has
   the name now. A namespace that a team creates again under the name of a
   purged namespace has a new ID. So it reads no change log row, no counter
   and no redaction record of the purged one.
3. **Take the number after every other row lock.** Each write takes the
   number as the last statement before the commit, after every other row
   lock of the transaction: the item rows and the inventory row.
   So the counter row lock holds only for the commit. The benchmark below
   shows that the place of the statement halves the cost.
4. **SQLite.** The SQLite adapter uses the same table and the same
   statement. Its write transactions already run one at a time, so the
   counter adds one row write and no wait.
5. **Restore.** The counter rows are in the same table class as the change
   log rows. So a restore replaces both or neither, and never leaves a
   counter below the highest sequence of a row.
6. **Why not a database sequence.** A PostgreSQL sequence gives its number
   at `nextval`, not at the commit. Transaction A gets 10, transaction B gets
   11, and B commits first. A reader that pages after 11 never reads 10. A
   read barrier can fix this: each row records its transaction ID
   (`xid8`), and a read returns only the rows below
   `pg_snapshot_xmin(pg_current_snapshot())`. But then one long transaction
   anywhere in the installation stops every reader of the log, SQLite has no
   such barrier, so the two adapters differ, and the barrier needs its own
   conformance cases for a race that tests find with difficulty. The
   counter row has none of these costs. Its cost is the wait of decision 7.
7. **Cost: benchmark.** pgbench 18.6, PostgreSQL 18.6, a 4-CPU container,
   `fsync` and `synchronous_commit` on, 32 clients, 15 seconds for each run.
   The transaction updates two unit rows, reads an index two times, and
   inserts one change log row.

   | Variant | 1 namespace | 64 namespaces |
   | --- | --- | --- |
   | Sequence (`nextval`) | 5,711 tps, 5.6 ms | 5,641 tps, 5.7 ms |
   | Counter row, taken last | 1,173 tps, 27.3 ms | 4,928 tps, 6.5 ms |
   | Counter row, taken first | 617 tps, 51.8 ms | 4,571 tps, 7.0 ms |

   With 1 client in 1 namespace, the sequence gives 1,025 tps at 0.98 ms and
   the counter row 896 tps at 1.12 ms. With 4 clients, 2,860 and 1,635 tps.

   So the counter row limits the inventory writes of one namespace to
   approximately 1,170 each second on this machine, and adds approximately
   0.14 ms to a write with no wait. Writes in different namespaces do not
   wait for each other (4,928 tps against 5,641). An inventory write occurs
   at the end of an update, at the finalize of a state version, and at a
   create, rename, delete, restore or purge. The start of an update changes
   only the lock of the stack, so it appends no change log row. A namespace
   with 100 updates that run at the same time makes fewer than 100 such
   writes each second. Audited writes (create, rename, delete, restore, purge) already
   hold the namespace row `FOR UPDATE`, so they already wait for each other.
   A write that changes only the lock of a unit appends no change log row,
   because the lock is not history (`Change.Entry`). The release load run
   (ADR 40) measures the counter on its largest namespace.

### G2. The route of a redaction record

8. **The route takes the namespace.** The route of ADR 151 decision 28
   becomes:

   `GET /api/inventory/namespaces/{namespace}/redactions/{redaction_id}`

   It reads `RedactionLog.GetRedaction(ctx, ns, id)` with the namespace of
   the path. A record of another namespace answers 404, the same 404 as an
   unknown ID, as ADR 149 decision 4 says for every read by ID.
9. **The action, and one 404 rule for reads by ID.** The route needs
   `namespace:read` on the namespace of the path. Then the handler finds
   the owner of the item of the record (decision 18). For a unit, it finds
   the unit by its tool and stable ID in the namespace of the path: the
   live unit, or the soft-deleted unit. When no such unit exists, the
   handler answers 404. Then it checks `unit:read` on that unit with its
   stable ID. The routes of this decision and of decision 20 use the same
   rule, as ADR 149 decision 4 says: an unknown unit, a unit of another
   namespace, a purged unit, and a unit on which the caller does not have
   the action all answer the same 404. So the answer never tells that the
   object exists. These routes do not use the resolver of the deleted-units
   routes as it is: that resolver finds only deleted units, and it answers
   403 to a caller without the action. The history responses of each tool
   give the redaction ID, and the caller knows the namespace of the unit,
   so a client has both parts of the path.

### G3. The item key record in the transaction of the item

10. **Two steps: prepare the key, then create the item with it.**
    This ADR replaces `ItemSealer.NewItemKey` with:

    ```go
    // PrepareItemKey makes a random item key for ref, wraps it, and writes
    // the key blob item-keys/<id>. It writes no record. It returns the
    // record for the create port of the item, ErrKeyNotConfigured when no
    // key-encryption key is configured, and ErrInvalidItemRef.
    PrepareItemKey(ctx context.Context, ref ItemRef) (ItemKey, error)
    ```

    It takes no fence: it writes only a blob, as the upload of a checkpoint
    blob comes before its row (ADR 37). A blob with no record is an orphan,
    and the orphan cleanup deletes it after 24 hours (ADR 151 decision 22).

    History writes need a key-encryption key. Encryption at rest
    is on by default, as the object stores of the large cloud providers do.
    So a running server never gets `ErrKeyNotConfigured` from
    `PrepareItemKey`:
    - `dustpan.NewServer` refuses to build a server without
      `WithSecretWrapper`, as it does now.
    - `opendustpan serve` refuses to start without a key, and stops before
      it opens the database with this error:
      `no key-encryption key is configured: set --generate-key-encryption-key
      (DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true) to make a key file with no
      prompt, or set --key-encryption-key-file
      (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) or DUSTPAN_KEY_ENCRYPTION_KEY`.
    - The escape hatch for development is
      `--generate-key-encryption-key`. On the first start, the server makes
      a local key file. On each start with this setting, the server logs
      this warning: `the key-encryption key is a local file that this
      server made: for production, set --key-encryption-key-file or
      DUSTPAN_KEY_ENCRYPTION_KEY`.
11. **Each create port takes the record.** Each port method that creates a
    history item takes the prepared record as the parameter
    `key secret.ItemKey`, and writes it in its fenced transaction with the
    item row:
    - `stack.UpdateStore.CreateUpdate(ctx, u, key, fence)`, for an update
      and for a preview, which gets an item key for its events
      (ADR 151 decision 6),
    - `workspace.StateVersionStore.CreateStateVersion(ctx, sv, key, fence, ...)`,
      at the declare step (ADR 151 decision 7),
    - the methods of `job.ArtifactStore` that create a plan or a version of
      a job log,
    - the create of a comment (ADR 198 decision 3).

    The store checks that `key.Ref` names the item that it creates (owner,
    kind and item ID), and returns `secret.ErrInvalidItemRef` otherwise. A
    create that finds the item already stored keeps the stored record and
    returns the stored item. The prepared blob is then an orphan.
12. **`ItemKeyIndex.CreateItemKey` leaves the port.** Only the create ports
    write a key record. `ItemKeyIndex` keeps `GetItemKey`, `ShredItemKey`,
    `ListItemKeys` and `ListItemKeyWrapperIDs`. `secrettest.RunItemKeys`
    gets the records it needs from a fixture method `SeedItemKey`, which is
    part of the fixture of the suite and not of the port. The suite of each
    create port gets these cases:
    - The record exists after the create.
    - A create with a stale fence leaves no item and no record.
    - A key of another item answers `ErrInvalidItemRef`.
    - A repeated create keeps the first record.

### G4. Client-encrypted items and the blame check

13. **An opaque item counts as compared only after the blame cursor passed
    it.** ADR 150 decision 9 marks a client-encrypted item `opaque` and
    writes no resource rows for it. Blame never reads the content of an
    opaque item. The check of ADR 151 decision 15.2 becomes:
    - **The item is opaque:** the redaction can continue when the blame
      cursor is at or past the item. Before that, the server does not know
      that the item is opaque, so the check answers 409
      (`history.ErrBlamePending`).
    - **The item is readable:** the redaction can continue when the blame
      cursor is at or past the next state item, opaque or readable, as
      ADR 151 decision 15.2 says.
    - **A later readable item with a redacted base:** after such a
      redaction, a later readable item can find that the last readable item
      before it is redacted. This occurs only when opaque items come between
      the two. The blame of
      that later item then writes no resource rows and gets the mark
      `base-redacted`, as an opaque item gets the mark `opaque`. Blame says
      that it cannot tell the changes, and it never shows changes that did
      not occur.
14. **Why.** The common case is a team that turns on client-side encryption
    because a plain value leaked in an old state version. After that, every
    new state version is opaque. A rule that waits for a later readable item
    would block that redaction for ever. The rule of decision 13 lets the
    redaction continue, and it fails closed in each case where the server
    does not know the kind of the item. `historytest.OpenTofuSituations`
    gets two situations: `SituationOpaqueNext` (a readable item before an
    opaque item: redaction continues) and `SituationOpaqueUncompared` (an
    opaque item before the cursor passed it: 409). The blame conformance
    suite of `workspace.BlameStore` (`workspacehistorytest`) gets the case
    `SituationBaseRedacted` (a readable item after opaque items whose base
    is redacted: no rows, mark `base-redacted`), because the blame writes
    the rows and the mark. Safe delete already treats an opaque state
    version as a state with resources (ADR 153 decision 10).

### G5. Item kinds, item owners and nested wraps

15. **Item kinds are an open set that the server registers.**
    `secret.ItemKind.Valid` checks only the syntax: 1 to 32 characters,
    lowercase letters, digits and hyphens, with a letter first. The `secret`
    package declares no constant for a kind or an owner kind. Each kind and
    each owner kind is an entry of the registry, and the domain package
    that owns it declares it. The
    `history` package keeps a registry (`history.Kinds`) that
    `dustpan.NewServer` builds from the kinds of each domain package. Each
    entry names the kind, its owner kind and its `Redactor`:

    | Kind | Owner kind | Redactor | Source |
    | --- | --- | --- | --- |
    | `state` | `unit` | each tool | ADR 151 |
    | `plan` | `unit` | job model | ADR 151, ADR 152 |
    | `job-log` | `unit` | job model | ADR 151, ADR 152 |
    | `comment` | `change-order` | change orders | ADR 198 |

    A redaction of a kind that the registry does not hold answers 400
    (`history.ErrInvalidItem`). The registry is a value that the server
    passes to its services, not a global variable, so a program that embeds
    the SDK (ADR 35) can add a kind without a change to the `history`
    package.
16. **No SQL `CHECK` on a kind.** The columns of the item kind and of the
    owner kind are `TEXT` with no `CHECK`. The Go code checks a kind before
    each write. A new kind then needs no migration. A reader that finds a
    kind that this server does not register still applies each shred, each
    gap of `history.Gaps` and each restore step: they need only the key IDs
    and the blob keys, never the kind. So a restore of a backup from a
    server with more kinds never brings back shredded content.
17. **Owners are an open set too.** `history.ItemRef` names an owner in
    place of a unit:

    ```go
    type Owner struct {
        Kind      OwnerKind      // "unit" or "change-order"
        Namespace namespace.Name
        Tool      string         // only for the owner kind "unit"
        ID        string         // the unit ID or the change order ID
    }
    ```

    `history.UnitOwner(u Unit)` makes the owner of a unit. The scope of the
    item key is the namespace and the owner ID. Owner IDs have their own ID
    prefixes (ADR 19), so two owners never share a scope. `OwnerKind` has
    the same syntax rule as `ItemKind`. For the kind `comment`, the scope
    of the item key is the namespace and the ID of the change order that
    owns the comment. PR #846 aligns ADR 198 decision 3 to this rule.
18. **Each owner kind names its object, its actions and its end.**

    | Owner kind | Object of the read | Redact action | End of its items |
    | --- | --- | --- | --- |
    | `unit` | the unit | `unit:redact` on the unit | the purge of the unit, with a tombstone (ADR 151 decision 8) |
    | `change-order` | each unit of the change order | `unit:redact` on each unit (ADR 198 decision 3) | the purge of the namespace |

    A tombstone names an owner, not only a unit, so the purge of a
    namespace writes one tombstone for each owner that is not a unit, in the
    same steps.
19. **Nested wraps for content of more than one item.** ADR 128 decision 19
    seals a transcript result that holds content of more than one history
    item, so that a shred of any one item key makes the result unreadable.
    `secret.ItemSealer` gets two methods for this:

    ```go
    // SealNested seals the plaintext in r under a fresh data key, and wraps
    // that data key under the item key of each ref in turn. Opening needs
    // every item key. It returns ErrInvalidItemRef for no ref, for more than
    // MaxNestedItems refs, for a repeated ref or for refs of more than one
    // namespace, ErrKeyNotFound, and
    // ErrShredded when one key is shredded.
    SealNested(ctx context.Context, refs []ItemRef, binding []byte, r io.Reader) (io.Reader, error)
    // OpenNested opens a stream that SealNested sealed for refs and
    // binding. It reads every record before any blob, so it returns
    // ErrShredded when one key is shredded. It returns ErrDecrypt for
    // other refs, another binding, or a changed stream.
    OpenNested(ctx context.Context, refs []ItemRef, binding []byte, r io.Reader) (io.Reader, error)
    ```

    The rules:
    - The sealer sorts the refs by `ItemRef.String()`, so the order of the
      caller does not change the result. `MaxNestedItems` is 8.
    - The data key seals the content with the stream format of
      `SealContent`. The first wrap seals the data key under the first item
      key. Each next wrap seals the output of the wrap before it under the
      next item key. The associated data of each wrap holds the binding, the
      ref and its place in the list. The stream header holds the last wrap.
    - The caller stores the refs with the sealed content, as ADR 128
      decision 19 says. The sealed content is not a history item. It has no
      key blob and no item key of its own, so the scrub and the orphan
      cleanup do not see it.
    - The cost of an open is one record read, one key blob read and one
      unwrap of 32 bytes for each ref, and one stream decrypt.

### G6. A unit at a time, by its ID

20. **One route form for both tools, in the inventory API.**

    `GET /api/inventory/namespaces/{namespace}/units/{tool}/{unit_id}/state-at/{at}`

    It names the unit by its tool and its stable ID, so it reaches a renamed
    unit and a soft-deleted unit (ADR 150 decision 14). A unit of another
    namespace, an unknown unit and a purged unit answer 404 (ADR 149
    decision 4). A time before the first state item answers 404 with the
    time of the first item (ADR 150 decision 12).
21. **Each tool serves the content through a port.** The inventory API
    calls the port `inventory.UnitStateAt` of the tool, as it calls the
    lifecycle of the tool for a restore. Each tool implements it with its
    `StateAtReader` and its masked state read (ADR 41). The response holds:
    the tool, the unit ID, the name at the time `at` and the name now, the
    mark `deleted` with the time of the delete, the item and its actor, the
    running update for Pulumi (ADR 150 decision 12), the redaction when the
    item is redacted (ADR 150 decision 13), and the masked state in the
    shape of the tool, with its format (`pulumi-deployment` or
    `opentofu-state`). The inventory API never parses the state, so the tool
    models stay apart (ADR 13).
22. **The action.** `unit:read` on the unit with its stable ID, with the
    404 rule of decision 9: the handler finds the live or soft-deleted unit
    in the namespace of the path, and answers 404 when it finds none or
    when the caller does not have the action. Grants stay until the purge
    (ADR 47), so this check works for a soft-deleted unit.
23. **The tool routes stay.** The routes of ADR 150 decision 15 stay, by
    name, for live units only. Each tool CLI and each tool shape keep them.

### G7. The conformance suite of the inventory change log

24. **`inventorytest.RunHistory`.** Every store that implements
    `inventory.HistoryReader` runs it. Its fixture is the reader, a
    `namespace.Store`, and a fixture method
    `AppendChange(ctx, c, fence) (inventory.Change, error)` that runs the
    append code of the store in a fenced transaction of its own. It is not a
    port method: each tool appends in its own write transaction. The cases:

    | Case | What it checks |
    | --- | --- |
    | `AppendAssignsSeq` | The store sets `Seq` and ignores the value of the caller. The sequences of one namespace increase. |
    | `SeqPerNamespace` | Two namespaces have their own sequences. A read of one never returns a row of the other. |
    | `ConcurrentAppends` | 16 goroutines append 20 rows each in one namespace. The 320 sequences are distinct, and `ListChanges` returns all 320 in order. |
    | `PagingUnderAppends` | A reader pages with the cursor and a limit of 7 while appends run, and reads on after they end. It reads each row one time, and skips none. A database sequence with no read barrier can fail this case. |
    | `Until` | A row at `until` is in the answer. A row after it is not. |
    | `CursorAndLimit` | The limit holds. The cursor excludes its own row. The page after the last row is empty. |
    | `UnknownNamespace` | `ListChanges` and `UnitsAt` answer an empty list and no error. |
    | `UnitsAtMatchesReplay` | For a time before the first row, at each row, 1 ms before each row, and after the last row, `UnitsAt` equals `ReplayAt` over all rows. |
    | `ClockSkew` | A row with an earlier time and a higher sequence wins for its unit. |
    | `DeletedAndPurged` | A deleted or purged unit leaves `UnitsAt`. A restored unit comes back. A renamed unit has its new name. |
    | `PurgeKeepsRows` | After a `purged` row, `ListChanges` still returns every row of the unit. |
    | `NamespaceRecreated` | Purge, then a new namespace with the same name sees no row. `ListChanges` and `UnitsAt` of the new namespace answer only its own rows. |
    | `StaleFence` | An append with a stale fence answers `namespace.ErrFenced` and writes no row. |

    The suite allows gaps in a sequence, so that a custom store can use
    another mechanism that meets the cases.
25. **The cases that need the tool tables go in `storetest`.** Each tool
    write that changes the inventory appends one row in its transaction,
    with the fields of the entry after the write. A write that changes only
    the lock appends no row. A write that fails on the fence appends no row.
    `inventorytest` has its own in-memory store in its test, as `historytest`
    has.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **ADR 150, decision 16:** "The store assigns the sequence number in the
  transaction of the write. It increases in the order of commits." The
  sequence is now per namespace, and a counter row in the write transaction
  assigns it (decisions 1 to 7).
- **ADR 150, decisions 14 and 15:** a request names the unit by its current
  name or its ID, and the routes take a name. The by-ID form is now the
  inventory route of decision 20, which also reaches a soft-deleted unit.
- **ADR 150, Consequences, open work item 5:** the inventory change log gets
  its suite, `inventorytest.RunHistory` (decisions 24 and 25).
- **ADR 151, decision 3:** "One SQL adapter implements the `ItemKeyIndex`
  port and the tables of each tool". The key record is now a parameter of
  each create port (decisions 10 to 12), and `ItemSealer.NewItemKey` is now
  `PrepareItemKey`.
- **ADR 151, decision 15.2:** the blame check now has the rule for opaque
  items (decision 13).
- **ADR 151, decision 28:** `GET /api/inventory/redactions/{redaction_id}` is
  now `GET /api/inventory/namespaces/{namespace}/redactions/{redaction_id}`
  (decisions 8 and 9).
- **ADR 151, Consequences:** "the kinds are `state`, `plan` and `job-log`
  for every tool". The kinds and the owner kinds are now open sets that the
  server registers (decisions 15 to 18), and `ItemSealer` gets
  `SealNested` and `OpenNested` (decision 19).

## Consequences

- The inventory writes of one namespace wait for each other at the commit.
  The benchmark gives approximately 1,170 writes each second for one
  namespace on a 4-CPU machine. The sizing guide states this limit.
- No port of this ADR has an adapter yet, so the port changes cost no
  adapter code.
- A new item kind or owner kind needs no migration and no change to the
  `secret` package. It needs a registry entry, a `Redactor` and a
  `historytest` situations table.
- A readable state version that follows opaque state versions whose base is
  redacted has no blame rows. The history response shows the mark
  `base-redacted`.
- A client reads a redaction record and a unit at a time by ID with the
  namespace in the path. A 404 never tells that the object exists in
  another namespace.
- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. `inventorytest.RunHistory` with its in-memory store, then the counter
     table, the change log tables and the adapters.
  2. The port change of `ItemSealer` and `ItemKeyIndex`, then the `key`
     parameter of each create port, with the cases of decision 12.
  3. The kind and owner registry, `history.Owner`, and the syntax rule of
     `ItemKind` and `OwnerKind`.
  4. `SealNested` and `OpenNested` with a test that seals under two item
     keys, shreds one, and fails to open.
  5. The blame rule for opaque items, with its three situations.
  6. The routes of decisions 8 and 20, with the port
     `inventory.UnitStateAt` of each tool.
