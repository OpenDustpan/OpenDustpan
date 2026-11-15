# 322. No migrations or compatibility code before the first tag

Status: accepted · 2026-11-02. Decisions 1, 2, 4 and 5 are owner decisions. Decisions 3 and 6 are PM decisions, taken on the owner's behalf, marked "(PM)"

Changed by:

- ADR 362 changes decision 5: the `.sha256` files of the baselines are absent until the first tag, and the release generates them.
- ADR 325 changes decision 1: the test of the one baseline becomes `TestMigrationFilesFollowTheRecord`, with a second form after the first tag.
- ADR 325 changes decision 5: the disabled checks skip only while the release record lists no shipped migration file.
- ADR 325 changes decision 6: the release record turns the checks on, and the collapse script refuses from the record.
- ADR 326 extends decision 1: the name-order placement rule applies to the `queries.sql` file of each dialect too.

Changes:

- Changes ADR 73 decision 6: until the first tag, each dialect keeps one numbered baseline. A change to the schema edits it in place. The runner records and compares the SHA-256 of each migration.
- Extends ADR 73 decision 4: until the first tag, no pull request writes migration, upgrade, alias or data-conversion code. This holds for each shape that no tag shipped.

## Context

ADR 73 decision 6 makes each change to an unreleased schema a new numbered
migration file, frozen until the collapse before a tag. Many pull requests
run in parallel in merge trains. Two pull requests that each add a migration
take the same next number, and the second must take a new number after the
first merges. Each such collision costs a new round of the gates.

The project owner gave these directions on 2 November 2026, in this order:

```text
1. "since we don't have even a single user yet we can delete the migrations
and API versioning altogether"
2. "a single DB schema file and API V1 can accept breaking changes"
3. "keep the capability for after the release ... stop building
compatibility and migrations it's only costing us"
4. "just disable them for now/t.Skip with a todo and a github issue"
5. "we don't need to rip it out, we can just stop using them"
6. "shouldn't be annoying to use during development"
7. "shouldn't cause production issues in the future"
8. "remove automated reset altogether and make reset easily accessible for
DEV"
```

The owner also said that this thread is one ADR or a few. This ADR records
the schema part. ADR 323 records the API part. ADR 324 records the schema
mismatch and the dev reset.

OpenDustpan has no tag, no user, no installation and no data. On `main` at
b76ada6, each dialect had 64 numbered migrations, from `0100_baseline.sql` to
`0167_inventory_last_release.sql`. The collapse made `0168_baseline.sql` from
them. The runner recorded the version, the name and the
minimum binary version of each migration. It compared versions only. A
database that recorded a version with other SQL in it opened with no error.

## Options considered

| Option | Why it was refused |
| --- | --- |
| Remove the frozen-file check and the numbered-file runner | The owner wants the capability after the release (direction 3) and the checks disabled, not removed (direction 4). |
| A new schema file and a new schema-hash mechanism beside the runner | The owner said to stop using the mechanism, not to replace it (direction 5). The runner already has a place for a content check. |
| An automatic reset of a dev database on a schema mismatch | The owner removed it (direction 8). ADR 324 records the explicit reset. |
| A reset setting for a production database | It can remove production data (direction 7). ADR 324 gives no reset outside dev. |

## Decision

1. **One baseline for each dialect until the first tag (owner).** Each
   dialect keeps one numbered migration file, the baseline
   `pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`. Its number is above
   each file that it replaced, as ADR 73 decision 6 says. Until the first
   tag, a pull request changes the schema by an edit of the baseline of each
   dialect, in place. It adds no numbered migration. It puts a new object in
   its place in the name order of the baseline, not at the end, so that
   parallel changes edit different lines.
   `TestOneMigrationFileUntilTheFirstTag` fails when a `migrations` directory
   holds another file.
2. **No migration or compatibility code for untagged shapes (owner).**
   Until the first tag, no pull request writes migration, upgrade,
   compatibility, alias or data-conversion code for a schema or an API shape
   that no tag shipped. Reviewers refuse such code. The tool-compatible APIs
   keep the rule of ADR 73 decision 2.
3. **One content check in the runner (PM).** The runner and the store open
   path stay. The runner records the SHA-256 of the SQL of each migration in
   a new `sha256` column of `schema_migrations`. `migrate.Check` refuses a
   recorded version whose name or SHA-256 differs from the embedded file,
   and a recorded value that is not 64 lowercase hex digits. The refusal is
   the existing `ErrSchemaDiverged`. A database of an earlier version of the
   baseline therefore does not open. ADR 324 decides what each command says
   then. Each adapter reads and compares the row again on its own, as a
   defense-in-depth layer (ADR 250).
4. **The runner stays proven (owner).** `TestRunnerAppliesAFixtureChain`
   runs the numbered-migration runner of each dialect on two fixture
   migrations in `testdata/runner-chain`. It checks the order, the record of
   each SHA-256, the second run that applies nothing, and the refusal of an
   edited migration.
5. **The checks are disabled, not removed (owner).** These tests skip with a
   TODO until the first tag: `TestFrozenMigrations` of each dialect and the
   two baseline round-trip tests. A hand edit of the baseline does not keep
   the form of a dump, so the round-trip tests must stop too. The hash files
   stay in the tree. Issue #2305 lists each disabled check with its file and
   line. No Makefile target, script or CI step checked the migration numbers.
6. **At the first tag (PM).** The baseline of each dialect becomes the first
   migration of the tag, with the same bytes. `make regenerate-baseline`
   writes it again only when the merge session collapses new numbered files
   of a train. The checks of decision 5 come back, as issue #2305 says. A
   change after the tag is a new numbered file, and ADR 73 decision 6
   applies to it again. The content check of decision 3 stays for each
   migration.

## Consequences

- The SHA-256 covers each byte of the baseline. An edit of a comment alone
  also makes each dev database refuse, and its developer runs
  `opendustpan dev reset`.
- A regeneration with no change of the schema gives the same bytes: the
  header names no Git ref, and the baseline keeps its version when it
  replaces no other file.
- Parallel pull requests that change the schema edit the same file. Git
  merges edits to different tables without a conflict. A person resolves a
  conflict in the same lines by hand. The baseline is source, not a
  generated file.
- The merge session runs `make regenerate-baseline` on `main` after the last
  train that adds a numbered migration. The baseline then holds every
  migration of `main`. The script refuses a collapse when a tag exists.
- The tests that applied one numbered migration to the data of an earlier
  migration go with those migrations. No data conversion remains to test.
- A backup records the schema version of the baseline for each untagged
  build, and the restore compares the version only. A restore cannot see an
  edit of the baseline between the backup and the restore. A backup of an
  untagged build has no support (ADR 73 decision 6). Issue #2306 tracks a
  check of the SHA-256 in the restore.
- The upgrade check (ADR 33) compares migration names. Two untagged builds
  have the same name for the baseline, so the upgrade check cannot see an
  edit of the baseline. The server refuses such a database at start.
