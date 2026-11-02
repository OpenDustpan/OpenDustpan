# 73. Until 1.0, a change can break everything except the tool-compatible APIs

Status: accepted · 2026-10-17 · supersedes ADR 21 and ADR 72 in part (see "Superseded decisions")

Changed by:

- ADR 322 changes decision 6: until the first tag, one schema file for each
  dialect and no numbered migration.

## Context

ADR 21 says that the API rules in `CLAUDE.md` apply from the first tag. Those
rules forbid the removal or the rename of a field that a released console
uses, and they ask for a deprecation first. ADR 72 decisions 10 and 11 set
the upgrade rules for the database schema.

Agent sessions spend effort on migrations for schema that no tag shipped, and
on code that keeps old shapes alive. No user depends on these shapes. The
repository has no tag on 2026-10-17.

The project owner decided that a change can break any part of the project
until 1.0. The owner also decided that agents stop spending effort on migrations
for unreleased schema and on legacy or compatibility code. The real Pulumi
CLI and the real OpenTofu CLI do not change for us. So the APIs that they
call must still match what they send and expect.

The owner asked for less effort on migrations, not for broken installs of a
tagged release. The owner did not set the migration rule. The integrator session proposed
decision 6, and the PM session accepted it on 2026-10-17.

## Decision

1. **Until 1.0, a change can break what is ours.** These can change in a way
   that breaks a caller, with no deprecation period:
   - the tool-neutral APIs, for example the inventory API and the
     installation API,
   - the Go SDK in `pkg/`,
   - the commands and flags of the `opendustpan` binary,
   - the settings of the `opendustpan` binary, with one limit. A setting
     that a tag shipped keeps working after an upgrade, or the binary
     refuses to start. The error names the setting and its replacement. The
     binary never ignores such a setting without an error.
   - the database schema and the backup format, with the limits of decisions 5
     and 6.
2. **The tool-compatible APIs cannot break.** The Pulumi Cloud-compatible API,
   the TFE-compatible API and the OpenTofu HTTP backend must match what each
   supported version of the real CLI sends and expects. This rule has no end
   date. A change to these APIs passes `make e2e` against the real CLIs.
3. **The console change merges before the next server tag.** A server change
   that breaks an API that the console calls has a matching console change.
   The console change merges before the next server tag. Between the two
   merges, `main` of either repository can be broken. A server tag pins a
   console tag that works with it (ADR 21).
4. **No compatibility code for unreleased shapes.** Do not write a shim, an
   alias, a second read path or a data conversion for a shape that no tag
   shipped. Remove such code when you find it.
5. **Released shapes keep their upgrade path.** A schema shape or a backup
   format that a tag shipped follows ADR 72 decisions 10 and 11. A setting
   that a tag shipped follows the limit in decision 1.
   An installation of a tagged release upgrades with no manual step.
6. **Migrations of an unreleased schema.** An unreleased shape is
   a schema shape that no tag shipped.
   - A change to an unreleased shape is a new numbered migration file. The
     file can drop, rename or make again a table or a column. It does not
     convert the data that an untagged build wrote.
   - An applied migration file stays frozen until the collapse below.
     Developer databases, CI databases and `TestFrozenMigrations` depend on
     it. The hashes of `TestFrozenMigrations` change only in the collapse.
   - A migration file that a tag shipped never changes.
   - A database or a backup that an untagged build wrote has no support. When
     a migration fails on such a database, the operator makes a new database.
   - Before each tag, the files that no tag shipped collapse into one file
     for that tag. Before the first tag, all files collapse into the
     baseline of each dialect. This collapse comes before each tag, not
     only once (ADR 72 decision 7).
   - The collapsed file gets a version number above the number of each file
     that it replaces. So a database from an untagged build records versions
     that the new binary does not embed, and it lacks the collapsed file. The
     migration check then refuses the database with `ErrSchemaDiverged`. If
     the collapsed file used a number that the database already records, the
     check would find no pending migration and accept the wrong schema.
7. **At 1.0, a new ADR sets the contract rules again** for every API, the
   Go SDK and the schema.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 21 | "Version", first sentences on the API rules | Decisions 1 to 3 | Before 1.0, only the tool-compatible APIs keep the contract rules. The console and the tool-neutral APIs can change. |
| 72 | 7, "one-time exception" | Decision 6 | The migration files that no tag shipped collapse into one file before each tag, not only before the first tag. |

## Consequences

- `CLAUDE.md` replaces the section "The APIs are contracts" with the rules
  of decisions 1 to 3.
- An API change that the console needs no longer waits for a server release.
  The console change merges before the next server tag.
- Reviews refuse compatibility code for unreleased shapes.
- An operator who runs an untagged build can lose the data of that build.
  The release notes and the install docs say to run tagged releases only.
- The e2e suite stays the gate for the tool-compatible APIs. A change that
  only `make check` tests can still break the real CLIs.
