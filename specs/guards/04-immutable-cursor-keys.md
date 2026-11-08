# Guard 04: a page cursor never sorts on a value that changes

Status: ready for implementation. Open questions: none.

## Problem

A keyset page reads the rows after the cursor in the sort order of the
query. If a write can change a sort column of a row, the row can move from
after the cursor to before it, or back, between two pages. A reader then
skips the row or reads it twice, and a count that uses the same key moves
too. ADR 166 decision 3 already gives the rule for one port: "a page cursor
of names could skip or repeat rows". No check applies it to the others.

Real examples:

1. Issue #2926. The delivery history of a subscription pages on
   `(next_at_ms, id)` (`ListNotificationDeliveriesOfSubscriptionAfter` and
   `CountNotificationDeliveriesOfSubscriptionBefore`, in
   `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`;
   `pkg/notify/ports.go:418-429`, `DeliveryCursor`). Each attempt rewrites
   `next_at_ms` (`pkg/store/sqlite/notifications.go:1139-1143`). A delivery
   in retry moves past the cursor and the later pages skip it. Open PR
   #2946 pages on the creation time.
2. The group list pages on `principal_groups.name_key` (`ListGroups` and
   `ListGroupsOfPrincipal`, both dialects), and a rename of a group writes
   `name_key` (`UPDATE principal_groups SET name = $1, name_key = $2, ...`
   in `pkg/store/postgres/queries.sql:1566`). Found by the scan of this
   spec.
3. The Pulumi stack list pages on `(project, name)`
   (`ListStackPageInNamespace` and `ListStackPageInProject`, both
   dialects), and a stack rename writes `stacks.name`
   (`pkg/store/postgres/queries.sql:202`). Found by the scan of this spec.

## The check

A static test of the SQL of both stores. It needs no database. It runs in
`go test -race ./...`.

### Where it lives

A new test-only package `internal/store/pagekeys` (a `_test.go` file and its
`leak_test.go`, as CLAUDE.md asks for each test package). It reads, with
`os.Root`:

- `pkg/store/sqlite/queries.sql` and `pkg/store/postgres/queries.sql`: each
  block from `-- name:` to the next one;
- the Go files of `pkg/store/sqlite` and `pkg/store/postgres` (not test
  files, not `internal/db`): each string literal and each constant string
  concatenation, parsed with `go/parser`.

### What it enumerates

- **Keyset queries.** A SELECT with ORDER BY and LIMIT in which a column of
  the ORDER BY is compared with a parameter, alone (`col > $2`,
  `col < sqlc.arg('after_ms')::bigint`) or in a row value
  (`(a, b) > ($2, $3)`), with or without `COLLATE`. The compared columns are
  the cursor key. The test resolves each column to its table through the
  FROM and JOIN aliases of the query.
- **Writes.** Each `UPDATE <table> SET <columns>` and each
  `INSERT ... ON CONFLICT ... DO UPDATE SET <columns>`, in both sources.
  The set columns of a table are its mutable columns.

Today the scan finds 25 keyset queries in the PostgreSQL file and 21 in the
SQLite file whose cursor parameter has a name that starts with `after`,
`before` or `cursor`, and some more whose parameter has another name (see
"Allowed exceptions").

### What it asserts

1. Each column of the cursor key of each keyset query is not a mutable
   column of its table, in either dialect.
2. Each dynamic page query of a Go file is on the table of dynamic page
   queries in the test (the file, the function, the table and the key
   columns), and its key passes rule 1. A Go function with a literal that
   holds ORDER BY and LIMIT, and that is on neither that table nor the
   exception list, fails the test. Today about 10 functions hold such a
   literal (for example `unitPageQuery` in `inventory_query.go`,
   `queryDeletedPage` in `inventory.go`, the job list in `jobs.go`).
3. Each failure names the query, the dialect, the column and the write that
   changes it.

A column that the test cannot resolve (an expression, a column of a
subquery) fails the test with "cannot resolve". So the test never passes a
key that it did not check.

### The behavior row (for each fix)

A fix of a known row adds a conformance case to the suite of its port, with
one shared helper `storetest.CheckPageStable`: write N rows, read the first
page, run the write that changed the old key (an attempt, a rename), read the
other pages, and assert that every row comes exactly once. The helper makes
the case short, so each paging port can have it.

## Allowed exceptions

`internal/store/pagekeys/testdata/exceptions.txt`. One line for each
exception: the query name or the Go function, a tab, the kind, a tab, the
reason. Two kinds:

- `not-a-page`: a keyset-shaped query that is not a page a client walks,
  for example a point-in-time read (`RunningUpdateAt`, `WorkspaceStateAt`)
  or a queue claim (`ClaimNotificationDeliveries`,
  `DueBreakGlassReviews`). The reason says what the query reads instead.
- `stable-by-rule`: a page whose key is written by a write that the domain
  forbids while the list exists. The reason names that rule and its test.

The test refuses a line with an empty reason, a query that does not exist,
and a line that the scan does not need. No inline marker. The owner reviews
each change of the file.

## Acceptance criteria

1. Mutation rows, as SQL text in `testdata`, each fail the scan: a page on a
   column that an `UPDATE ... SET` writes; a page on a column that an
   upsert writes; a page with a row-value cursor that holds such a column;
   a page in a Go literal with no entry on the dynamic table; a key column
   that the scan cannot resolve.
2. A page on an ID, a sequence or a creation time that no write sets passes.
3. The scan finds the three known keys of today (see "Existing offenders")
   in both dialects. This keeps the scan from turning into a check that
   finds nothing.
4. The known list only shrinks, through `internal/knownlist` of spec 01.
5. Each fix adds a `CheckPageStable` case to the suite of its port, which
   fails on the old key.
6. `make check` passes.

## Existing offenders

From a run of a prototype of the scan on the two `queries.sql` files
(named queries only):

| Key | Queries (each in both dialects) | Write that changes it |
|---|---|---|
| `notification_deliveries.next_at_ms` | `ListNotificationDeliveriesOfSubscriptionAfter`, `CountNotificationDeliveriesOfSubscriptionBefore` | each delivery attempt |
| `principal_groups.name_key` | `ListGroups`, `ListGroupsOfPrincipal` | a group rename |
| `stacks.name` | `ListStackPageInNamespace`, `ListStackPageInProject` | a stack rename |

That is 3 keys, 6 queries, 12 query rows over the two dialects. The scan of
the Go files is not run yet. Task 1 adds it and may add rows.

These go on `internal/store/pagekeys/testdata/known.txt` with the limit 12
(or less, if PR #2946 merges first). The list only shrinks.

## Tasks

1. **The scan.** Add `internal/store/pagekeys` with the SQL reader, the Go
   reader, the dynamic table, the mutation rows, the exception list (the
   `not-a-page` lines) and the known list. Add `storetest.CheckPageStable`.
   Acceptance: criteria 1 to 4 and 6. Size: about 700 lines. Conflict area:
   new files only.
2. **Delivery history.** Page on the creation time and the ID, as PR #2946
   does, with a `CheckPageStable` case in the notify suite (record an
   attempt between two pages). Acceptance: 4 lines leave the known list.
   Size: as PR #2946. Conflict area: notify store files, both baselines.
3. **Group list and stack list.** Page each one on an immutable key: the
   group ID and the stack ID, in the order of creation, with a prefix
   filter for a search by name where the route has one. Add a
   `CheckPageStable` case for a rename in each suite. The stack list is a
   route of the Pulumi Cloud-compatible API, so the change passes
   `make e2e` with more stacks than one page. Acceptance: 8 lines leave
   the known list and the limit is 0. Size: about 400 lines. Conflict
   area: the principal store, the stack store, both baselines (edited in
   place, ADR 322), and the console repository for the order of the group
   list.

## Constraints

- ADR 166 decision 3: a cursor of names can skip or repeat rows. This spec
  applies the rule to every page.
- ADR 210 decision 10: lists page with an opaque cursor. The wire cursor
  stays opaque. Only the key inside it changes.
- ADR 322: a new column or index goes into the baseline of each dialect in
  place, in name order. No numbered migration.
- ADR 73: the Pulumi Cloud-compatible API is a contract. The stack list
  change passes `make e2e`. The group list is a tool-neutral route, which
  can change before 1.0. The console change merges before the next server
  tag.
- CLAUDE.md "SQL": a fix adds its query block in name order in
  `queries.sql`, and regenerates `internal/db` with `make generate`.
- CLAUDE.md "Tests": the scan reads files with `os.Root` and has no
  network and no database.

## Decisions taken in this spec (industry default)

- **The check is static.** A page key is a property of the SQL, and a
  static scan sees every query, also one that no test pages through. The
  behavior row proves each fix.
- **Any comparison of an ORDER BY column with a parameter makes a keyset
  query, whatever the name of the parameter.** So a new page cannot escape
  the scan with a new parameter name. A query that is not a page goes on
  the exception list with its reason.
- **The fix pages on an immutable key in creation order.** This is the
  usual keyset design (an ID or a creation time and an ID). A list that a
  person reads by name gets the name order from the client or from a
  filter, not from the cursor. A person sees the new order of the group
  list and of the stack list (task 3), so this item is reported to the PM
  for the owner. The guard (task 1) does not wait for it: the two keys stay
  on the known list until task 3 lands.

## Open questions

None.
