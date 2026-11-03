# 325. The release record turns the shipped-migration checks on

Status: accepted · 2026-11-03. A PM decision, taken on the owner's behalf. It changes the mechanism of ADRs 322 and 323 only. The owner decisions of those ADRs do not change

Changes:

- Changes ADR 322 decision 1: the test of the one baseline becomes `TestMigrationFilesFollowTheRecord`, with a second form after the first tag.
- Changes ADR 322 decision 5: the disabled checks skip only while the release record lists no shipped migration file.
- Changes ADR 322 decision 6: the release record turns the checks on, and the collapse script refuses from the record.
- Changes ADR 323 decision 2: the version test of the description skips only while the release record lists no shipped migration file.

## Context

ADR 322 decision 5 and ADR 323 decision 2 disable five checks until the
first tag: `TestFrozenMigrations` of each dialect, the two baseline round
trips and `TestVersionFollowsTheDescription`. Each skipped with no
condition. Only a person who read issue #2305 would turn them on again.

ADR 322 says that `make regenerate-baseline` refuses a collapse when a tag
exists. The script looked only at `testdata/tag-migrations/`, which a
person fills by hand after the tag. `TestOneMigrationFileUntilTheFirstTag`
stayed on after the tag, and it refuses the numbered migration that ADR 73
decision 6 asks for then.

After the first tag, the store compares the SHA-256 of each byte of each
migration. An edit of the shipped baseline in place, or a collapse on top of
the tag, makes every database of the tag refuse to open, with no migration
path. Architecture review round 9 found this (issue #2335). The owner said
that the decision "shouldn't cause production issues in the future".

## Options considered

| Option | Why it was refused |
| --- | --- |
| The tag copies in `pkg/store/<dialect>/testdata/tag-migrations/` | Two directories can disagree. `scripts/copy-tag-migrations.sh` reads the tag with `git archive`, so it runs after the tag exists, and the checks are off between the tag and the copy. |
| A Git tag, read with `git tag` or `git describe` | CI can use a shallow clone with no tags, and a test must not need Git history. The script keeps this check as a second layer only. |
| A constant such as `schema.FirstTag` | A test cannot give a constant another value, so no test can prove both states of each guard. It also cannot freeze each shipped file by its SHA-256. |
| An environment variable | A variable can be forgotten, and CI could run with it unset after the tag. |
| A build tag | A build tag can be forgotten too, and it splits the code that the tests see. |

## Decision

1. **The release record is the one fact.** The committed file
   `internal/releaserecord/shipped.txt` lists each migration file of each
   dialect at each tag. Each line is `<tag> <dialect> <file> <sha256>`.
   Lines that start with `#` and blank lines are comments. The package
   `internal/releaserecord` embeds and parses it. A test of any package
   reads it with no network and no Git history.
2. **The release writes it.** The release adds the lines of its tag in the
   commit that it tags. Each tag lists every migration file at the tag, so a
   file that an earlier tag shipped appears again under each later tag, with
   the same SHA-256. So the checks turn on in the same change that records
   the tag. A line never changes or goes after its tag.
3. **Not released: the current behavior stays.** While the record lists no
   file, the five checks skip. Each skip message names the check and the
   record. `DUSTPAN_TEST_DESCRIPTION_PIN=1` still runs the version test on
   demand. It cannot turn the test off. `TestMigrationFilesFollowTheRecord`
   refuses a `migrations` directory with a file other than the baseline.
4. **Released: the checks run.** When the record lists a file, the five
   checks run with no skip. `TestMigrationFilesFollowTheRecord` takes its
   second form: each listed file must be present with the listed SHA-256,
   and each other file must have a number above every listed file.
5. **The collapse refuses from the record.** `scripts/regenerate-baseline.sh`
   refuses when the record lists a file, and when the record is missing. It
   does this before it needs a database or a ref. As a second layer, it also
   refuses when the repository has a tag that matches `v[0-9]*`. The check
   of `testdata/tag-migrations/` stays.
6. **Fail closed.** A record that does not parse fails each guard. It never
   counts as "not released". The parser refuses an unknown dialect, a field
   outside its allowlist, a line that a tag lists twice, one file of a
   dialect with two SHA-256 values, and a tag that does not list a file of
   each dialect.
7. **A tag cannot lose its record.** Two layers stop an empty record or a
   later removal of its lines. Each stops it alone:
   - `scripts/release-build.sh <version>` runs
     `scripts/check-release-record.sh <version>` before it builds anything.
     The check refuses a version that is not `vMAJOR.MINOR.PATCH`, a version
     with no lines in the record, and lines whose files or SHA-256 values
     differ from the migration files of the work tree.
   - `TestTagCopiesHaveTheirLines` of each dialect runs before and after the
     first tag. Each `testdata/tag-migrations/<tag>/` directory must have
     the lines of `<tag>` in the record, with the SHA-256 of each copy, and
     each listed file must have a copy.
8. **Proof of both states.** The tests of `internal/releaserecord` give each
   state of the record to the decision, to the migration file check and to
   the tag copy check directly, with a record of one tag and a record of two
   tags. They run the collapse script and the release record check in a new
   repository with each state. They parse each guard. A guard that calls
   `Off` must start with `t.Parallel()` and then the `if` statement that
   skips on the reason from `Off`. Each later skip or `return` must be
   inside an `if` statement. A guard of the migration files or the tag
   copies has no skip and no `return`. A skip inside a helper function is
   out of the reach of this test.

## Consequences

- The release adds one step before the tag: run `make regenerate-baseline`
  so that the round trips pass, set `DocumentVersion` in
  `internal/api/apidesc/document.go` for the release, write the pin of the
  description with `-update-description-pin`, then write the lines of the
  record. `scripts/release-build.sh` refuses the build until the record
  has the lines. The gate in `docs/roadmap.md` lists the step.
- The record repeats the SHA-256 of each shipped file. A change that edits a
  shipped migration and its `.sha256` file together still fails, because
  the record holds the shipped value.
- Before the first tag, nothing changes for a developer. The baseline is
  edited in place, and `make regenerate-baseline` works as before.
