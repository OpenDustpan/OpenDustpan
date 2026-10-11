# 33. Upgrade checks

Status: accepted · 2026-10-11

## Context

[RFC 38](../rfc/0038-upgrade-checks.md) asks how an operator learns what an
upgrade will do. A brief of 2026-10-11 checked the code. It informed this ADR.

Both store adapters apply the embedded migrations when they open the database.
Neither adapter reads the versions that `schema_migrations` records and the
binary does not know. An older binary on a newer database starts and runs
without a warning. No command shows the pending migrations before the upgrade.
No upgrade test exists. The first release has no previous tag. Migrations only
go forward (ADR 21). The project deprecates no API now. ADR 30 adds backups. ADR 32 adds
metrics.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts RFC 38 with the scope below.

1. **Refuse a newer schema.** When a store opens, it reads the recorded
   migration versions before it applies anything. If the database records a
   version that the binary does not embed, the store returns `ErrSchemaNewer`
   and applies nothing. PostgreSQL does the check under the migration lock.
   This applies to every command that opens the store. The error names the
   schema version of the database, the schema version of the binary and the
   version of the binary. No flag turns the check off.
2. **Amendment to ADR 21.** The check of decision 1 joins the gate of ADR 21
   for `v0.1.0`. The gate gets this line: "The binary refuses to start on a
   database schema that is newer than it knows." ADR 21 stays as it is, because
   ADR 1 forbids edits to an accepted ADR. `docs/roadmap.md` gets a new item
   for the line. A `v0.1.0` binary must be able to refuse a later schema,
   because no later release can add this protection to `v0.1.0`.
3. **Check command.** `opendustpan upgrade check` reads the database and writes
   nothing. It does not create `schema_migrations`, apply a migration, open the
   blob store or read a key. It prints the binary version, the schema version of
   the database, the schema version that the binary needs, and the names of the
   pending migrations. It ends with one verdict: up to date, upgrade possible,
   or cannot start. When migrations are pending, it says to back up the
   database first and that no downgrade path exists. The exit code is 0 for the
   first two verdicts and 1 for the third and for errors. The command is a
   process-level tool (ADR 17, process settings). It has no REST endpoint and no
   console view.
4. **Deprecations.** The server has no usage ledger now. The first change that
   deprecates an API field, endpoint, flag or environment variable must do four
   things:
   - It sends the `Deprecation` header where the response shape allows it.
   - It logs "deprecated feature used" with the field `feature`.
   - It counts the use in the metric `dustpan_deprecated_use_total{feature}`
     (ADR 32).
   - It names the change in the release notes.

   A ledger that the check command reads is a later decision.
5. **Migration at start.** `serve` still applies the pending migrations at
   start. The install documents describe the order for several nodes. The first
   new node migrates. An older node that still runs does not stop. Its next
   start fails by decision 1.
6. **Downgrade.** The project supports no downgrade. There are no down
   migrations. To go back, the operator restores the backup made before the
   upgrade (ADR 30) into an empty database, and starts the older binary. The
   older binary refuses to start on the newer schema.
7. **Upgrade test.** `make upgrade-test` and a CI job build the newest `v*` tag
   that is an ancestor of HEAD. They start it on a new SQLite database and on
   PostgreSQL, write data with the real Pulumi CLI and OpenTofu, stop it, and
   start the current binary on the same database. The test checks that the
   data, the secrets and the held lock survive. It then starts the older binary
   on the upgraded database and checks the refusal of decision 1. When no tag
   exists, the test runs the empty-database case of ADR 21. The test is not part
   of `make check`.
8. **Frozen migrations.** From the `v0.1.0` tag, a test holds the SHA-256 of
   each released migration file. The test fails when a file changes. A new
   migration adds its hash in the same change.

## Consequences

- Open work for milestone 3:
  1. The version check in `store/internal/migrate`, called from the SQLite and
     PostgreSQL adapters. Unit tests cover equal, older, newer and gap cases.
     Each adapter test checks that a refusal leaves the database unchanged.
  2. A read-only inspect function in each adapter, and the `upgrade check`
     subcommand with tests on a fresh, an old and a newer SQLite database, and
     on PostgreSQL.
  3. `make upgrade-test`, the CI job that clones tags, and the empty-database
     case before the first tag.
  4. The frozen-migrations test, added with the `v0.1.0` tag.
  5. Install documents: the backup advice, the multi-node order and the restore
     path for a downgrade.
- The item for decision 2 goes into the gate of `v0.1.0` in `docs/roadmap.md`.
  The check of decision 1 is code for the first release. The other decisions
  stay in milestone 3.
- A binary older than this ADR does not refuse a newer schema. Only releases
  that include decision 1 protect their users.
- The RFC question "server command or console report" has the answer: a server
  command. The console can show deprecations later, through the API that the
  first deprecation adds.
- Follow-ups for other RFCs: an explicit `migrate` command and a
  `--migrate=never` flag (RFC 7), the usage ledger (when the first deprecation
  exists), and per-migration notes about data loss.
- The CI checkout is shallow by default. The upgrade job must clone the tags.
- The exit test of this part of milestone 3 is this. On Linux, the upgrade test
  starts the previous tag on SQLite and on PostgreSQL, writes data with both
  CLIs, and starts the current binary on the same database. Data, secrets and
  the held lock survive. The previous binary then refuses to start on the
  upgraded database, if it has the check of decision 1. Before the first tag,
  the empty-database case passes. A store test shows that a database with a
  schema newer than the binary stops every command, and that the refusal changes
  nothing. `opendustpan upgrade check` gives the right verdict and exit code on
  a fresh, an old and a newer database.
