# 39. Commands never touch the database

Status: accepted · 2026-10-11 · decisions 1 and 4 superseded in part by ADRs 237, 238 and 245

## Context

Some `opendustpan` commands open the database or the blob store directly:

- `namespace create` writes the namespace into the database. No REST route
  creates a namespace.
- `restore` loads a backup into the database (ADR 30, replaced by ADR 38).
- `upgrade check` reads the migration state of the database (ADR 33,
  decision 3).
- `backup check` reads the backup target directly (ADR 30, decision 10).

This has problems:

- An operator must reach the database or the bucket from the place where the
  command runs. In a safe environment, workstations cannot do that. Only the
  server can.
- The action has no authorization, no policy check and no audit (ADR 17).
- Two programs write the same data with different checks.

The project owner decided on 2026-10-11 that a command never touches the
database directly.

## Decision

1. **Only the server opens the data.** `opendustpan serve` is the only command
   that opens the database, the blob store, a backup target or the
   key-encryption key. Every other command is a client of the REST API. It
   needs only network access to the server.
2. **How a command connects.** A client command takes `--server` (the
   environment variable `DUSTPAN_SERVER`) and a token from an environment
   variable, never from a flag. It accepts `--ca-file` for a private
   certificate authority. `opendustpan backup` already works like this.
3. **Each command gets an API route.** A command that has no route gets one
   first, with its action and object in the route table (ADR 17):
   - `namespace create`: a route in the installation API that creates a
     namespace. On a new installation, the bootstrap admin token calls it.
   - `restore`: the routes of ADR 38.
   - `backup check`: a route that checks a backup in the configured target and
     answers the result. The server reads the target, not the command.
   - `upgrade check`: a route that answers the version of the server, the
     schema version of its database and the names of the applied migrations.
     The command compares that answer with the migrations in its own binary
     and prints the verdicts of ADR 33. The command writes nothing.
4. **A gate, not only a rule.** The client commands live in a package that
   depguard forbids to import `pkg/store/...`, the blob adapters, the backup
   target code and the key-encryption key code. Only the `serve` wiring may
   import them. A test fails when a command other than `serve` opens a store.
5. **Start-up checks stay in the server.** The server still applies migrations
   at start and refuses a newer schema (ADR 33, decisions 1 and 5). When the
   server cannot start, the operator reads its log. No command reads the
   database to find out why.

This ADR replaces ADR 33 decision 3 (the command reads the database and has
no REST endpoint) and the part of ADR 30 decision 10 that lets `backup check`
read the target without a server. With ADR 38, no command restores offline.

## Consequences

- An operator needs a server URL and a token, not database credentials, for
  every task.
- `upgrade check` needs a running server. For a server that does not start,
  the log is the source, and the error names the schema versions (ADR 33).
- Every administrative task gets authorization, policy, audit and an e2e step.
- The bootstrap admin token must work on an empty installation for the first
  namespace and for a restore.
- The code of the offline commands goes away. Their checks move into the
  server.
