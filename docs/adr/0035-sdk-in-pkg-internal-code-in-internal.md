# 35. The SDK lives in pkg/, internal code in internal/

Status: accepted · 2026-10-11 · supersedes ADR 2

## Context

ADR 2 makes the server an SDK with ports and adapters. It puts the SDK entry
point in the root package `dustpan` and each domain package at the repository
root. The root directory now holds the entry point, its tests, ten domain and
adapter directories, and the tool configuration. A reader cannot see which
directories are the public SDK and which are not.

The four HTTP adapters (`api/pulumi`, `api/tfe`, `api/inventory`,
`api/server`) are public Go packages, but only the entry point imports them.
No embedder needs them: an embedder mounts the handler that `NewServer`
returns. The HTTP APIs are contracts. Their Go packages are not.

No release of OpenDustpan exists yet, so no embedder depends on the current
import paths.

The project owner asked for this layout on 2026-10-11.

## Decision

This ADR keeps every decision of ADR 2 and changes only where the code lives.

1. The SDK lives under `pkg/`:
   - `pkg/dustpan` is the SDK entry point: `dustpan.NewServer` takes
     functional options for every replaceable port. It replaces the root
     package.
   - The domain packages and their ports: `pkg/namespace`, `pkg/stack`,
     `pkg/workspace`, `pkg/secret`, `pkg/identity`, `pkg/inventory`,
     `pkg/backup`, `pkg/blob`.
   - The conformance suites (`pkg/store/storetest`, `pkg/blob/blobtest`,
     `pkg/secret/secrettest`) and the built-in adapters (`pkg/store/sqlite`,
     `pkg/store/postgres`, `pkg/blob/fs`, `pkg/blob/s3`).
2. Code that is not part of the SDK lives under `internal/`. The HTTP
   adapters move to `internal/api/<tool>`. The Go toolchain then stops other
   modules from importing them.
3. `cmd/opendustpan` stays a thin main that wires the built-in adapters from
   flags and environment variables. `e2e/` stays at the root.
4. The module path stays `opendustpan.org/opendustpan`.
5. These parts of ADR 2 stay: we organize code by domain, adapters depend on
   the domain and never the reverse (depguard enforces it), every port ships a
   conformance suite, and we design no port before a feature needs it.

## Consequences

- Every import path changes once. Embedders import
  `opendustpan.org/opendustpan/pkg/dustpan` and the `pkg/...` ports.
- The public Go API is smaller: the HTTP adapters are no longer importable.
  The HTTP APIs themselves do not change.
- The repository root holds only the binary, the e2e suite, docs, scripts and
  tool configuration.
- `sqlc.yaml`, `.golangci.yml`, the Makefile and the layout in `AGENTS.md`
  name the new paths.
- This ADR supersedes ADR 2. The other decisions of ADR 2 continue here.
