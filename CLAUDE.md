# OpenDustpan: instructions for AI coding agents and contributors

OpenDustpan is an open-source, self-hostable backend for the Pulumi CLI: a
drop-in replacement for the managed service. It is not affiliated with Pulumi
Corporation. Say "works with the Pulumi CLI"; never use Pulumi's name, logo or
colours as ours.

Read `docs/adr/` before changing architecture. If a change contradicts an ADR,
write a new ADR first.

## Definition of done

A change is done only when `make check` passes with zero findings:

- `golangci-lint run ./...` reports nothing. Fix code, do not weaken
  `.golangci.yml`. A `//nolint:<linter> // <reason>` is allowed only with a
  specific linter and a real reason; expect it to be questioned in review.
- `go test -race ./...` passes, including the conformance suites.
- `sqlc diff` is clean (regenerate with `make generate`, never edit `internal/db`).

Do not report work as finished if any of these fail. Say what fails instead.

## Layout

```
dustpan.go, web.go   package dustpan: the SDK entry point (NewServer + options)
cmd/opendustpan/     the binary: flags, env, built-in adapters. Only place that may exit.
namespace/           domain: namespaces, home region, failover-version fencing
stack/               domain: projects, stacks (updates and checkpoints go here next)
identity/            ports: Authenticator, Authorizer; bootstrap StaticToken, AdminOnly
blob/                port: object storage; blob/fs adapter; blob/blobtest suite
store/               SQL adapters: sqlite/, postgres/; storetest/ conformance suite
api/pulumi/          HTTP adapter for the Pulumi CLI API (also used by the console)
internal/            helpers shared across packages, not part of the SDK
docs/adr/            architecture decision records
```

The web console lives in its own repository, `OpenDustpan/console` (ADR 8).

## Backend rules (Go)

- Domain packages (`namespace`, `stack`, `identity`, `blob`) never import
  `net/http`, `database/sql`, drivers, `store/...` or `api/...`. depguard enforces this.
- Interfaces (ports) are defined in the package that consumes them, kept small,
  and documented with the errors they return.
- Every port has a conformance suite (`*test` package). A new adapter must run
  it; a new port method must get conformance tests before any adapter implements it.
- Errors: sentinel `Err...` values in the domain package, wrapped with
  `fmt.Errorf("...: %w", err)`. Adapters translate driver errors (no rows,
  conflicts) into domain sentinels. Never return driver errors to callers.
- Writes inside a namespace carry a `namespace.Fence` and the store checks it in
  the same transaction. Never add a write path that skips the fence.
- SQL: hand-written per dialect. Fixed queries go in `store/<dialect>/queries.sql`
  and are generated with sqlc; dynamic queries are hand-written in the adapter.
  Schema changes are new numbered files in `store/<dialect>/migrations/`; never
  edit an applied migration. Timestamps are Unix milliseconds in `BIGINT`.
- HTTP: `net/http` and `ServeMux` only. No web frameworks, no ORMs, no
  testify (depguard bans them). Logging is `log/slog` with an injected logger.
- Tests: standard `testing` only, black-box `_test` packages, `t.Parallel()`,
  table-driven where it helps. Use `t.Context()`.
- The Pulumi CLI API is not a published spec. Any endpoint change must be
  checked against real CLI behaviour (the end-to-end suite, once it exists).

## The API is a contract

Two clients depend on `/api`: the Pulumi CLI and the console, which ships from
another repository on its own schedule.

- Never remove or rename a field, endpoint or status code that a released
  console or CLI version uses. Add new fields; deprecate old ones first.
- Response shapes are camelCase JSON, errors are `{"code": <status>, "message": "..."}`.
- An API change the console needs lands here first, released, then in the console.

## Voice (UI text, errors, docs)

Exact, short, calm. Sentence case. Name the thing and the number. Errors say
what failed and the one command that helps. No exclamation marks, no emoji.

## Working in this repo

- Ask before adding a dependency. Prefer the standard library.
- Keep changes small and reviewable; one concern per change.
- If you are unsure whether something is intended, ask rather than guess.
