# 274. Dialect-neutral store logic

Status: accepted · 2026-10-22. Decisions 1 to 7 are owner decisions. Supersedes two decisions of ADR 3 in part. Accepts RFC 104

Changed by:

- ADR 362 extends decisions 2 and 6: RFC 103 step 8 comes before step 7 of this ADR. MySQL waits for the end of the work of this ADR.

## Context

ADR 3 makes each SQL dialect a full adapter. Each dialect has its own
schema, migrations and queries, and the dialects share no code. Today the logic of
each area exists twice, once for SQLite and once for PostgreSQL. A third
dialect would make it three copies. SQLite is a production adapter, so the
copies are a permanent cost.

[RFC 104](../rfc/0104-dialect-neutral-store-logic.md) measures this state and
gives a target layout under `internal/store/`. The owner gave the direction
before the RFC: shared logic for each area, one SQLite runner that holds the
write lock, and a declarative table list for backup. The owner answered the
seven questions of the RFC on 22 October 2026. RFC 104 gives the reasoning
and the exact design. This ADR gives the decisions.

## Decision

1. **A generated shared `Querier`.** The generator writes the shared
   `q.Querier` interface in `internal/store/sqlstore/q`. Each dialect gets a
   generated conversion shim from its sqlc types. The generator compares the
   types of the two dialects with `go/types.Identical`. The package `q`
   imports no driver. A dialect still holds its own queries, schema and
   migrations.
2. **The work starts after step 8 of RFC 103.** The store adapters first move
   to `internal/store/` (ADR 272). The steps of this ADR then follow. A step
   that lands first would make the scripted move bigger.
3. **SQLite has one write connection and a read pool.** A Go semaphore admits
   the one writer. The wait has a limit. When the limit passes, the write
   fails closed with an error. It does not wait without end.
4. **The maintenance gate is a primitive of the `Runner` of each dialect.** A
   shared contract and a conformance case define it: entering maintenance mode
   waits for each write in flight. PostgreSQL keeps its advisory lock. The
   gate change is its own step, after the conformance case.
5. **The table list is central.** It lives in `internal/store/tables`, with
   each declaration in that one package. The format types (`Column`, `Kind`,
   `PortableTable`) stay in `pkg/backup`, because they are part of the backup
   format. Backup, restore, portable backup and dev data read the list.
6. **A third dialect lives in this repository only, for now.** `sqlstore`, `q`
   and the area packages stay internal. No dialect outside this repository
   can use them. A later ADR can open them, after a third dialect proves the
   contracts.
7. **One generated interface for each area.** The generator writes a narrow
   interface for each area from a section marker in `queries.sql`. An area
   package sees its own interface and the shared core queries only. The
   Pulumi CLI area then cannot call a query of the OpenTofu area, so the tool
   models stay separate.
8. **The rules for error codes and for the fence.** Only the `Classifier` of a
   dialect parses driver error codes. No driver error or code leaves the
   dialect package. One function, `sqlstore.Write`, checks the installation
   state and the fence in the transaction of each write. `ON CONFLICT DO
   NOTHING` with a check of the affected rows stays the first choice for a
   duplicate insert.

## Steps

Each step is one pull request. Each step keeps the behavior, and the existing
conformance suites pass with no change to a case, except where the step adds
a case. All steps wait for step 8 of RFC 103. RFC 104 gives the size, the risk
and the dependencies of each step.

| Step | Change |
| --- | --- |
| 1 | Align the generated types of sqlc in both dialects |
| 2 | `sqlcshim`, `sqlstore/q`, and the core of `sqlstore`, with no caller yet |
| 3 | The fence and the state check in `sqlstore.Write` |
| 4 | A conformance case: entering maintenance mode waits for writes in flight |
| 5 | `Runner.Gate`: the gate becomes a primitive of the `Runner` |
| 6 | The SQLite runner: two pools, the semaphore, the writer connections |
| 7 | The table list in `internal/store/tables`, read by backup, restore and dev data |
| 8 to 24 | One area for each step, from low risk to high risk. The stack and workspace areas go last |
| 25 | The shared order of the backup and restore steps in `sqlstore` |
| 26 | Delete the empty helpers. Update `CLAUDE.md`, `GLOSSARY.md` and `.golangci.yml` |

## Consequences

- The logic of an area exists once. A change of an area is one change, not
  two. A new dialect supplies its queries, migrations, `Runner`,
  `Classifier` and backup engine.
- The generated `Querier` makes a query change visible in a generated diff.
  The generator checks that the two dialects agree on each type.
- A restore, a backup and dev data cannot disagree on the list of tables.
- The steps that touch every write (3, 5 and 6) have the highest risk. Each
  has a conformance case, and the case lands first.
- `CLAUDE.md` changes in step 26: the layout lists `internal/store/sqlstore`,
  the area packages and `internal/store/tables`, and the rule on SQL names the
  `Classifier`.
- This ADR changes no HTTP API. The tool-compatible APIs stay as they are.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 3 | "Each dialect is its own adapter with its own schema, migrations and queries." | Decisions 1 and 7 | A dialect is not a full adapter. The area packages hold the logic. A dialect supplies its queries, migrations, `Runner`, `Classifier` and backup engine. The schema, migrations and queries stay in the dialect. |
| 3 | "Adapters never parse driver-specific error codes." | Decision 8 | Only the `Classifier` of a dialect parses driver error codes, and no driver error leaves the dialect package. |
