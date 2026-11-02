# 322. Until the first tag, one schema file for each dialect and one version of the API description

Status: accepted · 2026-11-02. Decisions 1, 6, 7 and 9 are owner decisions. Decisions 2 to 5 and 8, and the details of decision 7, are PM decisions, taken on the owner's behalf, marked "(PM)"

Changes:

- Changes ADR 73 decision 6: until the first tag, each dialect has one schema file and no numbered migration. The collapse before the first tag happens now. A change to the schema edits the schema file.
- Changes ADR 264 decision 5: until the first release, the description has one fixed version. A pin of the version does not name one change of the description.
- Supersedes ADR 275 decision 8: `info.version` does not change with each change of the description, and the test that enforced it goes.

## Context

ADR 73 decision 6 makes each change to an unreleased schema a new numbered
migration file, and freezes each applied file until the collapse before a
tag. ADR 275 decision 8 makes `info.version` of the API description rise
with each change of the description. A test compared the description with
the file `internal/api/apidesc/openapi.pin`.

Many pull requests run in parallel in merge trains. Two pull requests that
each add a migration take the same next number, and the second must take a
new number after the first merges. Two pull requests that each change the
description both write `openapi.pin` and both raise the version. Each such
collision costs a new round of the gates.

On 2 November 2026 the project owner decided: "Since we don't have even a
single user yet we can delete the migrations and API versioning
altogether ... As in we can have a single DB schema file and API V1 can
accept breaking changes."

OpenDustpan has no tag, no user, no installation and no data. Nothing must
be migrated, and nothing needs compatibility code (ADR 73 decision 4).

On `main` at 04c979d, each dialect had 62 numbered migrations, from
`0100_baseline.sql` to `0165_name_locks.sql`.

## Decision

1. **One schema file for each dialect until the first tag (owner).** The
   files are `pkg/store/sqlite/schema.sql` and
   `pkg/store/postgres/schema.sql`. A pull request changes the schema by an
   edit of the schema file of each dialect. It adds no numbered migration and
   converts no data. `sqlc` reads the schema file.
2. **The collapse (PM).** `make collapse-schema` writes the two schema files
   from the numbered migrations of a Git ref (default `origin/main`). For each
   dialect it applies the migrations to an empty database, writes the schema
   and the rows of that database as the schema file, applies the schema file
   to a second empty database, and fails unless the two databases have the
   same tables, columns, indexes, constraints, triggers, views and rows. It
   then deletes the numbered migrations. The pull request that adds this ADR
   runs it. The merge session runs it again on `main` after the last train
   that adds a numbered migration, so the schema files hold every migration
   of `main`.
3. **The version table (PM).** The migration runner of each adapter creates
   `schema_migrations` from `pkg/store/<dialect>/schema_migrations.sql`, and
   `sqlc` reads the same file. Each row holds the version, the name and the
   minimum binary version of a migration, and the SHA-256 of its SQL. The
   store records the schema file as the migration `1000_schema.sql`, at
   version 1000, which is above each number of the numbered migrations.
4. **The schema check (PM).** The check is security code (ADR 250). At each
   open, the store refuses a database that another build of the schema made,
   and changes nothing in it:
   - A database that records a version below 1000 is from the numbered
     migrations. The store refuses it with `ErrSchemaBeforeBaseline`.
   - A database that records version 1000 with another name, with another
     SHA-256, with no SHA-256, or with a value that is not 64 lowercase hex
     digits, is from another version of the schema file. The store refuses it
     with `ErrSchemaChanged`.
   - Each error says what failed and the one action: start the server on an
     empty database.

   Three layers each stop such a database alone: the baseline check of the
   versions, the comparison in `migrate.Check`, and a second read and
   comparison of the row in the adapter, after `migrate.Check`. An unknown
   form, a missing column or a read error refuses the database. The store
   never changes a database to make it fit the schema file.
5. **The runner, the numbered files and the frozen-file check stay idle until
   the first tag (PM).** `migrate.Load`, the minimum binary version, the
   instance check, `testdata/tag-migrations` and the tag-copy check stay in
   the code. `TestFrozenMigrations` fails when the `migrations` directory of
   a dialect holds a file. At the first tag:
   - The schema file of each dialect moves, with the same bytes, to
     `pkg/store/<dialect>/migrations/1000_schema.sql`. Its name and its
     SHA-256 do not change, so a database of the last untagged build opens.
   - `TestFrozenMigrations` pins the SHA-256 of each migration file again,
     and `sqlc` reads the `migrations` directory.
   - A change after the tag is a new numbered file above 1000, and ADR 73
     decision 6 applies to it.
   - The SHA-256 comparison of decision 4 stays for each migration.
6. **The tool-neutral API stays at `v1` (owner).** The API under
   `/api/dustpan/v1` and the other tool-neutral prefixes can break before 1.0
   with no new version in the path (ADR 73 decision 1, ADR 210 decision 8).
   The release notes list each break.
7. **One version of the API description until a release (owner, details
   PM).**
   `DocumentVersion` in `internal/api/apidesc/document.go` keeps the value
   that `main` has when this ADR merges. A change of the description does not
   change it. `openapi.pin` and `TestVersionFollowsTheDescription` go.
   `make clients-check` stays the check that the generated clients match the
   description. The release process sets the version of the first release.
   A later ADR sets the version rule from that release on.
8. **Generated files (PM).** The merge session of each train generates the
   API description, the clients and the golden files again. A pull request
   can generate them too. To resolve a conflict in a generated file, run the
   generator again on the merged code. Never edit a generated file by hand.
   The schema files are not generated files after the collapse of decision
   2: a person resolves a conflict in a schema file by hand, as for any
   source file.
9. **The tool-compatible APIs do not change (owner).** The Pulumi
   Cloud-compatible API, the TFE-compatible API and the OpenTofu HTTP backend
   keep the rule of ADR 73 decision 2. They match what each supported version
   of the real CLI sends and expects, and `make e2e` checks them.

## Consequences

- Parallel pull requests that change the schema edit the same file. Git
  merges edits to different tables without a conflict. Edits to the same
  lines conflict, and a person resolves them.
- A developer database, a CI database and a database from an untagged build
  stop opening after each change of a schema file. The operator starts the
  server on an empty database. This is the rule of ADR 73 decision 6 for an
  untagged build.
- The tests that applied one numbered migration to data from an earlier
  migration go with the migrations. No data conversion remains to test.
- The upgrade check (ADR 33) compares migration names. Until the first tag,
  two builds have the same name for the schema file, so the upgrade check
  cannot see a change of the schema file. The server refuses such a database
  at start.
- A backup records schema version 1000 for each untagged build. The restore
  compares the version, not the SHA-256, so it does not see a change of the
  schema file between the backup and the restore. A backup of an untagged
  build has no support (ADR 73 decision 6). A later change can add the
  SHA-256 of the schema file to the backup.
- The client version keeps rule A4 of RFC 99 and ADR 275 decision 3: the
  major and minor parts come from `info.version`. Until the first release,
  the major and minor parts do not change, and the patch part of a client
  is its own.
- `CLAUDE.md`, `CONTRIBUTING.md`, `docs/reference/api-description.md` and the
  release notes describe the schema files and the fixed version.
