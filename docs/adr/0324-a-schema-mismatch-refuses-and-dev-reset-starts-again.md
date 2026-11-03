# 324. A schema mismatch refuses, and `opendustpan dev reset` starts again

Status: accepted · 2026-11-02. Decisions 1, 2 and 5 are owner decisions. Decisions 3 and 4 are PM decisions, taken on the owner's behalf, marked "(PM)"

Changes:

- Extends ADR 237 decision 8: `opendustpan dev reset` moves the data of a dev database aside and makes the schema again.
- Extends ADR 237 decision 30: the refusal of a database with another schema names the step that helps for each command.

## Context

Until the first tag, a change to the schema edits the baseline of each
dialect in place (ADR 322). A database of an earlier version of the baseline
then records another SHA-256, and the store refuses it with
`ErrSchemaDiverged`. The baseline changes often. A developer must not have
to find and remove a database by hand after each pull.

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

ADR 322 records the schema part, and ADR 323 the API part. This ADR records
the schema mismatch and the dev reset.

`dev` keeps its data in a data directory with one SQLite database, `dev.db`,
and a blob directory, `blobs` (ADR 237 decision 9). The installation kind
marks the command that made a database (ADR 237 decisions 23 to 31).

## Options considered

| Option | Why it was refused |
| --- | --- |
| An automatic reset of a dev database on a schema mismatch | The owner removed it (direction 8). A reset that runs on its own can remove data that a person wanted to keep. |
| A setting `--schema-mismatch=reset` for `serve` | A setting that resets a production database can remove production data (direction 7). |
| A new schema-hash mechanism for the check | The runner already refuses a changed migration (ADR 322 decision 3). The owner said to stop using the mechanism, not to replace it (direction 5). |
| Remove the old data at a reset | A developer can need the data of the earlier build. A kept copy costs disk space only. |

## Decision

1. **Every command refuses a schema mismatch (owner).** On a database with
   another schema, `serve`, `dev` and each other command that opens the store
   refuse with `ErrSchemaDiverged`, or with `ErrSchemaBeforeBaseline` for a
   database of the numbered migrations. Nothing resets a database on its
   own. No setting changes this.
2. **One explicit reset for dev (owner).** `opendustpan dev reset
   [--data-dir <dir>]` starts a data directory of `dev` again. It does these
   steps, and each check refuses alone:
   - It refuses a directory without the key file of `dev`, a data directory
     that is a symbolic link, and a directory that a live `dev` server uses
     (`dev.addr`).
   - It refuses a database that is not a regular file, and a database with a
     second name (a hard link). It refuses a `blobs` directory that is a
     symbolic link, that another user owns, or that the group or others can
     access.
   - It reads the stored kind with `sqlite.StoredKind`: read-only, under the
     guard of the store, with no schema check. It refuses a database that
     another process holds, a database of `serve`, a database with no kind,
     and a value that is not a known kind. It then changes nothing.
   - It moves `dev.db`, its log files and `blobs` aside as
     `dev.db.reset.bak` and `blobs.reset.bak`. It first removes the copies of
     the reset before, so the directory keeps the data of one reset only.
   - It writes a WARN log line that names the copies. It then makes the
     schema of the binary on a new `dev.db`, claims the kind `dev`, and
     writes an audit entry of `@server` that names the copies.
3. **The refusal names the step that helps (PM).** The refusal of `dev`
   names the command to copy: `the schema changed: run opendustpan dev reset
   to start again. The old data is kept as dev.db.reset.bak in the data
   directory`. It adds `--data-dir` when `dev` ran with it. The refusal of
   `serve` never offers a reset: it says to restore a backup of the same
   build, or to set `--database` to a new database.
4. **The kind marker (PM).** The rules of ADR 237 stay: only `dev` claims the
   kind `dev`, and only on a database that no command claimed. A database of
   `serve` never becomes a dev database. `dev reset` claims `dev` only on the
   new database that it made.
5. **No risk to production (owner).** `dev reset` works on a data directory
   of `dev` and a database of the kind `dev` only. It runs no migration on an
   existing database, and it never opens a database of another kind for a
   write. `serve` has no reset. At and after the first tag, the refusal of
   `serve` stays the same code. The numbered migrations of ADR 322 decision 6
   come back for production, and the checks of issue #2305 come back with
   them.

## Consequences

- A developer runs `opendustpan dev reset` after a pull that changed the
  baseline. The key file, the token and the certificate stay.
- The tests cover each layer: a database of `serve`, a database with no
  kind, an unknown kind, a garbled kind and no installation row are refused.
  So are a directory without the key file, a linked data directory, a
  symbolic link and a hard link of the database, a linked or open `blobs`
  directory, a database in use and a live server. A reset keeps the copy and names it in the log line and the audit
  entry, and a second reset replaces the copy of the first only. The refusal
  of `dev` names the command, and the refusal of `serve` does not.
- The dev server guide shows the command.
