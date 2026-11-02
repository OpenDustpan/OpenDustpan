# Release notes: changes since the last build

OpenDustpan has no tagged release yet. This page lists the breaking changes
that the first release notes must hold.

## Breaking changes

### One baseline migration for each database

- The 62 numbered migrations of each store dialect are now one baseline
  migration, `0166_baseline.sql`, in `pkg/store/sqlite/migrations/` and
  `pkg/store/postgres/migrations/`.
- Until the first tag, a change to the schema edits the baseline in place.
- The server records the SHA-256 of each migration. It refuses at start a
  database that records another SHA-256 or another name for the same
  version, and changes nothing in it. The error is
  `database schema and binary have diverged`.
- A database from the earlier numbered migrations records a schema version
  below 166. The server refuses it with `database schema version N is older
  than baseline schema version 166`.
- A backup of an earlier build does not restore.
- What to do:
  - For `serve`, restore a backup of the same build, or set `--database` to a
    new database. Run tagged releases only. An untagged build has no upgrade
    path.
  - For `dev`, run `opendustpan dev reset`. It keeps the old data as
    `dev.db.reset.bak` and `blobs.reset.bak` in the data directory.

### `opendustpan dev reset`

- A new command starts a dev data directory again on an empty database. It
  works only on a database that `opendustpan dev` made, and it keeps the
  data of the last reset. Nothing resets a database on its own.

### The API description keeps one version

- Until the first release, `info.version` of the API description stays at
  one value. A change of the description does not change it.
- The tool-neutral API under `/api/dustpan/v1` can change in a way that breaks
  a client before 1.0, and its path stays the same. These release notes list
  each break.
- The Pulumi Cloud-compatible API, the TFE-compatible API and the OpenTofu
  HTTP backend do not change.
