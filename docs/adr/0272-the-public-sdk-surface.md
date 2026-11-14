# 272. The public SDK surface

Status: accepted · 2026-10-22. Decisions 1 to 3 are owner decisions. Supersedes decision 1 of ADR 35 in part. Amends ADR 2 and ADR 35 (the rule on ports and services). Accepts RFC 103

Changed by:

- ADR 364 extends decision 4: the care for the exported surface applies to packages under `internal/` too, not only to the SDK.

## Context

ADR 35 puts the SDK in `pkg/`. It lists the entry point, the domain packages,
the conformance suites and the built-in adapters. Since then, each new package
went into `pkg/`. On `main` at bbdde60, 135 importable packages hold 6,800
exported names. The port closure of `pkg/dustpan` reaches only 40 packages. At
1.0, each exported name in an importable package is a promise. No one can say
which names are a promise today.

[RFC 103](../rfc/0103-the-public-sdk-surface.md) measures this state. It gives a
rule that a test can apply to a package. The owner decided these points on
22 October 2026: shrink the SDK before the first tag, keep the store ports and
their suites in the SDK, and fail CI when an exported `pkg/` signature names
an `internal/` type. OpenDustpan has not shipped. Breaking changes need no
migration path (ADR 73).

The owner answered the three questions of RFC 103 on 22 October 2026.

## Decision

1. **The built-in adapters are not SDK.** The adapters for SQLite, PostgreSQL,
   blob storage on a file system and on S3, the log sink, notifications, the
   forge for GitHub and Cedar move to `internal/`. `pkg/dustpan` gets a
   constructor for each one: `OpenSQLite`, `OpenPostgres`, `FileBlobs`,
   `S3Blobs` and the like. The names are not final until the pull request that
   adds them. An embedder gets the built-in adapters through these functions.
   The adapter types are not a promise. The ports and the conformance suites
   stay in the SDK, so a third party can write an adapter and prove it.
   This decision supersedes the list of built-in adapters in decision 1 of
   ADR 35.
2. **A domain package splits into a port package and a service.** For each
   of the 30 packages that hold both ports and a service:
   - `pkg/<domain>` keeps the types that the port closure reaches, the
     sentinel errors, the error types, the ports and the conformance suite.
   - The service moves to `internal/<domain>svc`.

   The rule of ADR 2 and ADR 35 that a domain package has "a small public
   service" becomes: a domain has a public port package and an internal
   service. The rule of `CLAUDE.md` "Define interfaces (ports) in the package
   that consumes them" becomes: a port lives in the SDK package of its domain.
   The service that consumes the port lives in `internal/<domain>svc`. The
   depguard `domain` rule covers the `pkg/` domain packages and the
   `internal/<domain>svc` packages. The packages that the rule did not list
   (`cron`, `idempotency`, `manuallock` and `profile`) join it in the same
   change.
3. **A checked-in snapshot of the SDK API.** The CI check `make sdk-surface`
   also writes `internal/tools/sdksurface/api.txt`, with one line for each
   exported SDK name and its signature. CI fails when the file is not up to
   date. A change to an SDK signature then shows in the diff of the pull
   request.
4. **The rule for a package under `pkg/`.** A package is SDK only when it is
   `pkg/dustpan`, when it declares a type that the port closure reaches, or
   when it is the conformance suite of a port that the closure reaches. Any
   other package moves to `internal/`. No exported signature in `pkg/` names a
   type from `internal/`. One more rule applies: a package that another Go
   module of this repository imports stays in `pkg/` and is SDK. Go lets only
   code inside the same module tree import `internal/`, so a wire contract
   between two modules must be public. Such a package is frozen like any other
   SDK package, and the same CI check covers it. `pkg/runner/protocol` is the
   first case: the server module, the runner module and the launcher share it.
   A path that an earlier ADR names is the location at that time. This ADR
   decides the location now. RFC 103 gives the exact definition of the port
   closure, the move plan and the CI check. This ADR adopts them.

## Consequences

- The SDK shrinks to `pkg/dustpan`, the port packages and the conformance
  suites. The store adapters (about 550 exported methods) are no longer a
  promise at 1.0.
- A change of a store method or of a service is not a breaking change of the
  SDK. A change of a port is, and the snapshot shows it at review time.
- Each caller of a split package imports two packages: the types from
  `pkg/<domain>` and the service from `internal/<domain>svc`. The suffix
  `svc` makes an import alias unnecessary.
- The move is 30 splits and a number of whole-package moves, each in its own
  pull request. A split is not mechanical, so a reviewer reads it as code.
  RFC 103 gives the order. The baseline file of the CI check only gets
  shorter, and the last step deletes it.
- `CLAUDE.md` changes its layout section and its rule on ports. The layout
  lists `internal/<domain>svc` and the constructors of `pkg/dustpan`.
  `.golangci.yml`, `sqlc.yaml`, the `Makefile` and the glossary name the new
  paths in the pull requests that move the code.
- The eight features without a production caller (`stepup`, `cost`,
  `chatops`, `incident`, `oncall`, `source`, `launcher` and `dependency`)
  move to `internal/`. A package returns to `pkg/` in the pull request that
  makes `NewServer` take its port.
- This ADR changes no HTTP API. The tool-compatible APIs stay as they are.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 2 | "each with a small public service and the interfaces it consumes" | Decision 2 | A domain has a public port package and an internal service. |
| 35 | 1, the built-in adapters `pkg/store/sqlite`, `pkg/store/postgres`, `pkg/blob/fs`, `pkg/blob/s3` | Decision 1 | The built-in adapters live in `internal/`. `pkg/dustpan` has constructors for them. |
| 35 | 1, the domain packages "and their ports" | Decisions 2 and 4 | A package is SDK only by the rule of decision 4. A service lives in `internal/<domain>svc`. |
| 131 | The runner messages in `pkg/runner/protocol` | Decision 4 | No change. The protocol stays SDK. |
| 139 | 4, the runner as a nested module that requires the server module | Decision 4 | No change. The protocol stays SDK, so the nested module can import it. |
