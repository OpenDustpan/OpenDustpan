---
version: 1
---

Issue: #3125.

Short form: bug. Owner lane: M5 (store).

## Problem

**Persona: contributor.** Two pull requests that each edit the baseline of a
dialect both change its `.sql.sha256` file and conflict there, and a merge
can leave a hash that matches neither baseline. ADR 362 removed the files
until the first tag for this reason, but they are still on main, and they
are stale.

## Evidence

- ADR 362 decision 4 (owner, 2026-11-08): "The `.sha256` files of the
  schema baselines (ADR 322) are absent until the first tag. The release
  generates them." ADR 322 line 7: "ADR 362 changes decision 5: the
  `.sha256` files of the baselines are absent until the first tag, and the
  release generates them."
- ADR 362, Consequences: "A separate pull request deletes them. It also
  changes the code, tests and scripts that read them." and "The runner
  still records the SHA-256 of each migration in `schema_migrations`
  (ADR 322 decision 3)."
- On main: `pkg/store/postgres/migrations/0168_baseline.sql.sha256` and
  `pkg/store/sqlite/migrations/0168_baseline.sql.sha256`. The merge of
  #2554 left main's hash next to a baseline with the objects of the branch.
- Readers and writers on main (`rg -n 'sql\.sha256' --glob '!openspec/**' --glob '!docs/adr/**'`):
  - `scripts/auto-regen.sh:59-60` (two globs in the list of generated
    paths) and `write_hashes` (:205-216), called by `regenerate` (:229);
  - `pkg/store/sqlite/frozen_test.go:21` and
    `pkg/store/postgres/frozen_test.go:21`:
    `//go:embed migrations/*.sql migrations/*.sql.sha256`. The pattern
    fails the build when no hash file matches. `readFrozenHash` (:232)
    reads the embedded file. `TestFrozenMigrations` (:30) is off until the
    release record lists a shipped migration (:35);
  - `scripts/regenerate-baseline.sh:114` and
    `scripts/copy-tag-migrations.sh:37`, and the tag copies under
    `pkg/store/<dialect>/testdata/tag-migrations/`, handle the hash files of
    a tag. They skip a file that is absent (`[ -e "$file" ] || continue`)
    or read the tag, so they stay.
- `.github/workflows/release.yml` and `scripts/release-build.sh` write no
  `.sql.sha256` file. `scripts/check-release-record.sh:49` takes the SHA-256
  of each migration from the work tree into the release record.

## Fix boundary

- Delete both `0168_baseline.sql.sha256` files.
- `scripts/auto-regen.sh`: remove the two globs and `write_hashes`, and its
  call in `regenerate`.
- `pkg/store/sqlite/frozen_test.go` and `pkg/store/postgres/frozen_test.go`:
  the embed pattern takes `migrations/*.sql` only, and `readFrozenHash`
  reads `migrations/<name>.sha256` from the package directory with
  `os.ReadFile`, so that the package builds with no hash file. The test
  stays off until the release record lists a shipped migration, and then it
  fails on a migration with no hash file, as today.
- The runner keeps the SHA-256 that it records in `schema_migrations`.
- The open specs that ask for a write of a baseline `.sql.sha256` file stop
  asking for it (fallout list in the tasks).
- Out of scope: the release step that writes the hash files at the first
  tag. The release gate owns it (the owner's word starts each release), and
  it is a separate change.

## Check

`find pkg/store -path '*/testdata' -prune -o -name '*.sql.sha256' -print`
prints nothing, `go test ./pkg/store/sqlite/ -run 'Frozen|Migration' -count=1`
passes, and `make check` reports zero findings (Confirmed by: CI).

## ADR check

**ADR 362: Owner decisions of 8 November 2026: stable IDs, store order, MySQL, baseline hashes and portable backups** (owner-tagged, decision
4: "(owner, 2026-11-08)"). Quote: "The `.sha256` files of the schema
baselines (ADR 322) are absent until the first tag. The release generates
them." Effect: this change deletes them and the code that writes them.
Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag**
(decision 5 changed by ADR 362; decision 3 owner). Quote, its Changed-by
line: "ADR 362 changes decision 5: the `.sha256` files of the baselines are
absent until the first tag, and the release generates them." Effect: the
record of the SHA-256 in `schema_migrations` (decision 3) stays. Conflict:
none.

## Open questions

none. Considered: let `TestFrozenMigrations` read the hashes from the
release record instead of hash files (refused here: it changes how "the
release generates them" works, which is the release gate's question).
