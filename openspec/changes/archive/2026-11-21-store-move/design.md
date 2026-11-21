## Context

The proposal holds the problem, the user path, the fix boundary and the ADR
check. This file gives the structure.

## Two pull requests

- PR A adds `dustpan.OpenStore(ctx, database string, opts ...)`, which opens
  the built-in store from a `--database` form and answers a
  `*dustpan.BuiltinStore`. The handle keeps the adapter behind
  `dustpan.StorePorts`, so no exported signature names an adapter type. The
  caller closes a store that it opened with `OpenStore`.
  `dustpan.WithBuiltinStore(store)` sets every store option of `NewServer`
  from the handle. `dustpan.WithSQLite(path string, opts ...)` and
  `dustpan.WithPostgreSQL(dsn string, opts ...)` open and close the store in
  one option. `cmd/opendustpan` uses `OpenStore` and `WithBuiltinStore`, so
  the wiring of the built-in stores exists in one place. The 23
  `With...Store` options stay for a third-party store.
- PR B is a script run, with no hand-written change: `git mv` of the 28
  packages to `internal/store/`, a rewrite of the import paths, and
  `make generate`. The script is committed with the move. A conflict is never
  resolved by hand: the script runs again on the new main. Its branch mode
  ports an open branch after a merge of main into it.

## The importers of the moved packages

`scripts/move-store.sh` rewrites the import path in every tracked Go file that imports a moved package, tests and e2e included; no other edit in those files. On main (ac41a29) they are 218 Go files in 43 package directories outside `pkg/store` (`git grep -l -E '"opendustpan\.org/opendustpan/pkg/store/(sqlite|postgres|schema|internal)' -- '*.go' ':!pkg/store/*'`). Task 1.3 names each directory, so that the scope guard of the driver admits the rewrite.

## Why this structure

The constructors make the public SDK the only way in. An embedder can no
longer depend on an adapter method, so a change of a store method is not a
breaking change (ADR 272). Splitting logic (PR A) from the move (PR B) keeps
the scripted PR free of anything a reviewer must read as code.
