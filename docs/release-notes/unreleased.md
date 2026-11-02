# Release notes: changes since the last build

OpenDustpan has no tagged release yet. This page lists the breaking changes
that the first release notes must hold.

## Breaking changes

### One schema file for each database

- The database schema of each store dialect is now one file:
  `pkg/store/sqlite/schema.sql` and `pkg/store/postgres/schema.sql`. The 62
  numbered migration files of each dialect are gone.
- The server records the schema file as `1000_schema.sql`, at schema version
  1000, with the SHA-256 of the file.
- A database that an earlier build made does not open. The server refuses it
  at start and changes nothing in it:
  - A database from the numbered migrations records a schema version below
    1000. The error is `database schema version N is older than baseline
    schema version 1000`.
  - A database from another version of the schema file records another
    SHA-256. The error is `another build of the schema made the database`.
- A backup of an earlier build does not restore.
- What to do: start the server on an empty database. Run tagged releases only.
  An untagged build has no upgrade path.

### The API description keeps one version

- Until the first release, `info.version` of the API description stays at one
  value. A change of the description does not change it.
- The tool-neutral API under `/api/dustpan/v1` can change in a way that breaks
  a client before 1.0, and its path stays the same. These release notes list
  each break.
- The Pulumi Cloud-compatible API, the TFE-compatible API and the OpenTofu
  HTTP backend do not change.
