# 8. Server and console live in separate repositories

Status: accepted · 2026-10-09 · amends the repository layout in ADR 5. The version handshake decided by ADR 201

## Context

ADR 5 decoupled the console's build and hosting from the server but kept it in
the same repository under `web/`. In practice the two halves share no code,
use different toolchains and gates, and the npm tree under `web/node_modules`
even shipped Go files that `go build ./...` picked up.

## Decision

- The server (Go) is `OpenDustpan/OpenDustpan`, module `opendustpan.org/opendustpan`.
- The console (TypeScript) is `OpenDustpan/console`, package `@opendustpan/console`.
- Each repository has its own CI, gates, `CLAUDE.md` and releases.
- The HTTP API under `/api` is the contract between them. Server changes stay
  backward compatible with released consoles; an API change the console needs
  ships in the server first.
- Packaging that combines both (the omnibus container image or OS package) pins
  one server version and one console version.

## Consequences

- A feature spanning both is two pull requests, in order.
- The console cannot rely on unreleased server behavior; the console's
  end-to-end tests run against a released (or pinned) server build.
- A version handshake (for example the console reading `/api/capabilities`)
  will be needed once the API grows features the console depends on.
