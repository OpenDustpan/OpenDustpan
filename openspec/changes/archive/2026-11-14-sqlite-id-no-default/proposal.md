Issue: #2994.

## Problem

A person who writes a store query or a test can insert a namespace, a stack or
a blob purge record with no ID. In SQLite the insert succeeds with the empty
string `''`. PostgreSQL refuses the same insert because its ID column has no
default. In SQLite the unique index on `stacks.id` then lets one stack with `''`
exist, and child rows can refer to it. Only the trigger on `namespaces.id`
rejects `''`; `stacks` and `blob_purges` have no guard. A test or a new query can
therefore store a row whose stable ID is empty, and the two dialects answer
differently.

## Evidence

- `pkg/store/sqlite/migrations/0168_baseline.sql:23` `id TEXT NOT NULL DEFAULT ''`
  (`namespaces.id`).
- `pkg/store/sqlite/migrations/0168_baseline.sql:33` `id TEXT NOT NULL DEFAULT ''`
  (`stacks.id`).
- `pkg/store/sqlite/migrations/0168_baseline.sql:333`
  `namespace_id TEXT NOT NULL DEFAULT ''` (`blob_purges.namespace_id`).
- `pkg/store/postgres/migrations/0168_baseline.sql:756` `id text NOT NULL`
  (`namespaces.id`).
- `pkg/store/postgres/migrations/0168_baseline.sql:1264` `id text NOT NULL`
  (`stacks.id`).
- `pkg/store/postgres/migrations/0168_baseline.sql:122`
  `namespace_id text NOT NULL` (`blob_purges.namespace_id`).
- `pkg/store/sqlite/migrations/0168_baseline.sql:1491`
  `CREATE UNIQUE INDEX stacks_id ON stacks (id);`. It lets one row with `''`
  exist, not a second one.
- `pkg/store/sqlite/migrations/0168_baseline.sql:1705-1715` the triggers
  `namespaces_id_insert` and `namespaces_id_update` refuse an id that is not
  `ns-?*`. The empty string fails that test. `stacks` and `blob_purges` have no
  such trigger.
- Every ID column of the SQLite baseline with a default
  (`pkg/store/sqlite/migrations/0168_baseline.sql`): `namespaces.id` :23,
  `stacks.id` :33, `updates.token_id` :49, `inventory.last_release_lock_id` :88,
  `workspaces.lock_id` :98, `workspaces.current_state_version_id` :102,
  `workspaces.last_release_lock_id` :104, `state_versions.lock_id` :123,
  `restores.pre_backup_id` :146, `group_members.source_id` :205,
  `audit_entries.token_id` :242, `audit_entries.request_id` :253,
  `audit_entries.object_id` :256, `audit_entries.job_id` :256,
  `blob_purges.namespace_id` :333, `runner_claims.job_id` :766,
  `passkey_challenges.principal_id` :1358,
  `passkey_challenges.pending_id` :1359,
  `passkey_challenges.session_id` :1360.
  Only three differ from PostgreSQL: `namespaces.id`, `stacks.id` and
  `blob_purges.namespace_id`. The other sixteen are optional references where
  `''` means none, and PostgreSQL has the same `DEFAULT ''`. The fix leaves
  them unchanged.
- The issue's suggested reason for the default does not hold on this checkout.
  The SQLite baseline has two `ALTER TABLE ... ADD COLUMN` statements, at `:14`
  and `:16`, and they add `min_binary_version` and `sha256` to
  `schema_migrations`. No statement adds `namespaces.id`, `stacks.id` or
  `blob_purges.namespace_id`. Those columns sit inside their `CREATE TABLE` with
  a leading comma, for example `:23 , allow_forced_writes ... id TEXT NOT NULL DEFAULT ''`.
  A previous migration added each column with a default, and the baseline
  collapse kept the default.

## Fix boundary

**IN**

- `pkg/store/sqlite/migrations/0168_baseline.sql`: remove `DEFAULT ''` from
  `namespaces.id` (`:23`), `stacks.id` (`:33`) and `blob_purges.namespace_id`
  (`:333`). The columns stay `TEXT NOT NULL`. Edit in place.
- `pkg/store/sqlite/migrations/0168_baseline.sql.sha256`: the new digest.
- `pkg/store/sqlite/internal/db/queries.sql.go`: regenerate with
  `make generate`. Every insert names the ID column, so the parameters do not
  change.
- `pkg/store/sqlite/sqlite_test.go`,
  `pkg/store/postgres/postgres_test.go`: add `TestInsertWithoutIDIsRefused`. It
  inserts a row with no ID column into `namespaces`, `stacks` and
  `blob_purges` and expects an error on both dialects.

**OUT**

- The other sixteen ID columns with `DEFAULT ''` that PostgreSQL also has. They
  are optional references, and a change would make the dialects differ.
- The `ALTER TABLE schema_migrations ADD COLUMN` statements. They are not ID
  columns.
- No numbered migration.
- No API, status code, audit entry or refusal change. No spec delta.

## Check

- Guard 1: an insert with no ID into `namespaces` is refused on both dialects.
  Test: `TestInsertWithoutIDIsRefused/namespaces`. Mutation: add `DEFAULT ''` to
  `namespaces.id` in the SQLite baseline; the SQLite insert succeeds, and the
  test fails.
- Guard 2: the same insert into `stacks` is refused. Test:
  `TestInsertWithoutIDIsRefused/stacks`. Mutation: add `DEFAULT ''` to
  `stacks.id`; the unique index takes the first row with `''`, and the test
  fails.
- Guard 3: the same insert into `blob_purges` is refused. Test:
  `TestInsertWithoutIDIsRefused/blob_purges`. Mutation: add `DEFAULT ''` to
  `blob_purges.namespace_id`; the test fails.
- Guard 4: a valid ID still inserts. The existing `TestConformance` cases for a
  namespace, a stack and a blob purge record pass. Mutation: add
  `CHECK (id = 'never')` to `namespaces.id`; the namespace round trip fails.

## ADR check

- **ADR 362 decision 1 (owner).** "Every stored reference to an entity uses the
  stable ID of the entity when a user can rename the entity, or delete and
  recreate it. A name is a label for people only." Changed by: ADR 363 (extends
  decision 2 only). Current rule: this ADR, with ADR 363. The fix refuses an
  empty stable ID. No conflict.
- **ADR 220 decision 1 (owner, by the title).** "Pooled rows with a namespace
  ID. All namespaces share one database and one schema. Each row that belongs
  to a namespace holds its namespace ID." Changed by: ADR 273 (decision 12),
  ADR 286 (words), ADR 363 (extends decisions 1, 2 and 3). Current rule: this
  ADR, with ADR 273, 286, 363. The fix makes `blob_purges.namespace_id`
  required. No conflict.
- **ADR 19 (not owner-tagged).** "A surrogate ID. Each stack has a stable ID,
  stored in the table `stacks`." Changed by: ADR 47 (extends: no move of a
  stack), ADR 248 (changes: the home region reads primary installation).
  Current rule: this ADR, with ADR 47, 248. The fix keeps the stack ID
  required. No conflict.
- **ADR 322 decision 1 (owner).** "One baseline for each dialect until the first
  tag (owner). ... a pull request changes the schema by an edit of the baseline
  of each dialect, in place. It adds no numbered migration." Changed by: ADR 325
  (decisions 1, 5, 6), ADR 326 (extends decision 1), ADR 362 (changes decision
  5). Current rule: this ADR, with ADR 325, 326, 362. The fix edits the SQLite
  baseline in place. No conflict.
- **ADR 274 decision 1.** "A generated shared `Querier`. ... A dialect still
  holds its own queries, schema and migrations." The text has no owner tag; the
  status line says decisions 1 to 7 are owner decisions. Changed by: ADR 362
  (extends decisions 2 and 6). Current rule: this ADR, with ADR 362. The fix
  stays in the SQLite dialect. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none
