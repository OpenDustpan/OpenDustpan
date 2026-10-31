# 2. The server is an SDK with ports and adapters

Status: superseded by ADR 35 · accepted 2026-10-08

Changed by:

- ADR 35 supersedes.

## Context

Operators range from a solo developer (SQLite, local disk) to enterprises with
their own databases, object stores, identity providers and secret managers. No
single backend choice fits all of them, and forks per backend would rot.

## Decision

- The root package `dustpan` is the server as a library: `dustpan.NewServer`
  takes functional options for every replaceable port, in the style of
  Temporal's server SDK. `cmd/opendustpan` is a thin main that wires the
  built-in adapters from flags and environment variables. Anyone needing a
  different adapter writes their own main.
- Code is organized by domain (bounded context) as deep modules: `namespace`,
  `stack`, `identity`, `blob`, each with a small public service and the
  interfaces (ports) it consumes. Adapters live elsewhere (`store/`, `blob/fs`,
  `api/`) and depend on the domain, never the reverse; depguard enforces it.
- Every port ships a conformance suite (`storetest`, `blobtest`) that every
  built-in adapter runs and custom adapters are expected to run.
- Seams are reserved, not built, for later ports (search, notifications, VCS
  hosts, secrets managers, LLM APIs): no interface is designed before a feature
  needs it.

## Consequences

- Ports are a public API. Changing one breaks custom adapters, so the SDK stays
  pre-1.0 until the ports settle, and ports stay small.
- The conformance suites are the contract; a port is not done without one.
- Built-in adapters are added in order of demand, starting with the
  single-node set: SQLite, local disk, static token.
