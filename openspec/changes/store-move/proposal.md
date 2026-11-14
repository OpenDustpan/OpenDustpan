## Why

Short proposal form (tech debt), with a user path because the public SDK
changes. Accepted by the PM on 2026-11-08 under the owner's mandate for the
week; it implements owner ADRs 272, 362 and 363 and changes none.

### Problem

`pkg/store/sqlite`, `pkg/store/postgres`, `pkg/store/schema` and the 25
packages under `pkg/store/internal/` are under `pkg/`, so they are public SDK.
At 1.0 each exported name becomes a promise. The two adapters have about 550
exported methods (ADR 272). The owner decided on 22 October 2026 that the SDK
is `pkg/dustpan`, the port packages and the conformance suites only, and that
the built-in adapters live in `internal/` with constructors in `pkg/dustpan`
(ADR 272 decision 1, owner).

The move also comes before the store rework (ADR 274, one shared copy of the
store logic and one table list for backup) and before the namespace ID change
(ADR 220 decisions 1 to 3), by the owner's order (ADR 362 decision 2, ADR 363).

### Evidence (main 3ee554a)

- 28 packages under `pkg/store/` other than `storetest`.
- 260 Go files import a store adapter path. Most are in the adapters
  themselves (about 350 import lines), then `pkg/dustpan` (65 files),
  `internal/api/server` (32), `e2e` (19) and `cmd/opendustpan` (12).
- `pkg/dustpan` has no constructor for a built-in store. Today an embedder
  imports `pkg/store/sqlite` and passes the store to 23 `With...Store`
  options one by one. `cmd/opendustpan` does the same wiring in its own code.

### User path

**Persona: an embedder** (a Go developer who builds their own server binary
with the SDK; add to the persona table of `openspec/project.md`).

1. Today: `st, err := sqlite.Open(ctx, path)`, then 23 options such as
   `dustpan.WithStackStore(st)`, `dustpan.WithJobStore(st)`.
2. After: `dustpan.NewServer(dustpan.WithSQLite(path), ...)` or
   `dustpan.WithPostgreSQL(dsn)`. One option opens the store and sets every
   store port. A wrong path or DSN fails `NewServer` with the error of the
   store, which names the setting.
3. A third-party store still works: the ports and `pkg/store/storetest` stay
   public, and the 23 options stay.
4. Docs: the SDK page (`docs/reference/`, the embedding section) shows the
   one-option form.

## What changes

### Fix boundary

- IN (PR B): move the 28 packages to `internal/store/...` with the same names; keep
  `pkg/store/storetest` (a conformance suite, SDK by ADR 272 decision 4); (PR A) add
  `dustpan.WithSQLite` and `dustpan.WithPostgreSQL`, built on the store
  wiring that `cmd/opendustpan` has today, so the wiring exists once; update
  `sqlc.yaml`, the Makefile, CI paths, `.golangci.yml`, the layout in
  `AGENTS.md` and the docs paths.
- OUT: the built-in blob, log sink, notify and forge adapters (RFC 103 step
  7); the SDK surface check (RFC 103 step 1); every logic change.

### How it lands

- **PR A (hand-written, normal review):** `dustpan.WithSQLite`,
  `dustpan.WithPostgreSQL`, and `cmd/opendustpan` using them, on today's
  paths. It touches `pkg/dustpan` and `cmd`, not the store internals, so it
  can land before or beside the #2989 fix.
- **PR B (scripted only, no logic):** the move, made by
  `scripts/move-store.sh` (`git mv`, import path rewrite, `make generate`),
  committed with it. A conflict is never resolved by hand: the script runs
  again on the new main. PR B lands after the #2989 fix, which edits the same
  store files by hand.
- **Branch mode of the script:** after a merge of main into an open branch,
  the script rewrites that branch's import paths and moved files, then runs
  `make generate`. Each open store PR is ported by the script, not by hand.
- **Triage before PR B:** 23 open PRs touch `pkg/store/` today (lead's count,
  8 Nov). Each is merged, closed as stale, or marked "port with branch mode".
  The PM decides (owner mandate of 8 Nov). The date of PR B follows the
  triage, not the drain.

## ADR check

- **ADR 272 decisions 1 to 4 (owner).** "The built-in adapters live in
  `internal/`. `pkg/dustpan` has constructors for them." Changes ADR 35
  decision 1 in part. This change implements it.
- **ADR 362 decision 2 (owner), ADR 363 decision 1 (owner).** RFC 103 step 8
  comes before ADR 274 step 7 and before ADR 220 decisions 1 to 3. This
  change is that step.
- **ADR 274 decision 2.** "The store adapters first move to `internal/store/`
  (ADR 272). The steps of this ADR then follow. A step that lands first would
  make the scripted move bigger." Followed.
- **ADR 3 and ADR 362 decision 3 (owner).** SQLite stays fully supported; it
  gets the same constructor as PostgreSQL.
- **ADR 322 (owner).** The baselines move with their package and are not
  edited, so their SHA-256 stays the same.

No conflict with an owner-tagged decision.

## Check

`go build ./...`, `make check` with zero findings, `make e2e`, and
the SHA-256 of each baseline is the same before and after the move (the
baselines are embedded relative to their package), `go list ./pkg/...` shows no `store/sqlite`, `store/postgres`, `store/schema`
or `store/internal` path. A test in `pkg/dustpan` starts a server with
`WithSQLite` alone and runs one stack update and one workspace state write.

## Open questions

none

Decided by the PM on 2026-11-08, reviewed by the implementation lead:
1. The move lands after the #2989 fix.
2. The names are `WithSQLite(path string, opts ...)` and
   `WithPostgreSQL(dsn string, opts ...)`.
3. The embedder persona is added to `openspec/project.md`.
