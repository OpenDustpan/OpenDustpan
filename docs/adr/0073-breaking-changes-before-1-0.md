# 73. Until 1.0, a change can break everything except the tool-compatible APIs

Status: accepted · 2026-10-17 · supersedes ADR 21 in part

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
   - the tool-neutral APIs, for example the inventory API, the installation
     API and the Catalog API,
   - the routes of the Pulumi Cloud-compatible API that only the console
     calls,
   - the Go SDK in `pkg/`,
   - the commands, flags and settings of the `opendustpan` binary,
   - the database schema and the backup format, with the limits of decision 6.
2. **The tool-compatible APIs cannot break.** The Pulumi Cloud-compatible API,
   the TFE-compatible API and the OpenTofu HTTP backend must match what each
   supported version of the real CLI sends and expects. This rule has no end
   date. A change to these APIs passes `make e2e` against the real CLIs.
3. **The console changes in the same push window.** A server change that
   breaks an API that the console calls has a matching console change. The
   two changes merge in the same push window. A server tag pins a console tag
   that works with it (ADR 21).
4. **No compatibility code for unreleased shapes.** Do not write a shim, an
   alias, a second read path or a data conversion for a shape that no tag
   shipped. Remove such code when you find it.
5. **Released shapes keep their upgrade path.** A schema shape, a backup
   format or a setting that a tag shipped follows ADR 72 decisions 10 and 11.
   An installation of a tagged release upgrades with no manual step.
6. **Migrations of an unreleased schema.** An unreleased shape is
   a schema shape that no tag shipped.
   - A change to an unreleased shape is a new numbered migration file. The
     file can drop, rename or make again a table or a column. It does not
     convert the data that an untagged build wrote.
   - Applied migration files stay frozen. Developer databases, CI databases
     and `TestFrozenMigrations` depend on them. The hashes of
     `TestFrozenMigrations` change only in the collapse below.
   - A database or a backup that an untagged build wrote has no support. When
     a migration fails on such a database, the operator makes a new database.
   - Before each tag, the files that no tag shipped can collapse into one
     file for that tag. Before the first tag, all files collapse into the
     baseline of each dialect (ADR 72 decision 7).
7. **At 1.0, a new ADR sets the contract rules again** for every API, the
   Go SDK and the schema.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 21 | "Version", first sentences on the API rules | Decisions 1 to 3 | Before 1.0, only the tool-compatible APIs keep the contract rules. The console and the tool-neutral APIs can change. |

## Consequences

- `CLAUDE.md` replaces the section "The APIs are contracts" with the rules
  of decisions 1 to 3.
- An API change that the console needs no longer waits for a server release.
  The console and the server change together.
- Reviews refuse compatibility code for unreleased shapes.
- An operator who runs an untagged build can lose the data of that build.
  The release notes and the install docs say to run tagged releases only.
- The e2e suite stays the gate for the tool-compatible APIs. A change that
  only `make check` tests can still break the real CLIs.
