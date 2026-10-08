# 4. Namespaces, home regions and failover-version fencing

Status: accepted · 2026-10-08

## Context

Pulumi Cloud's organizations are called namespaces here. Enterprises want
asynchronous geo-replication; everyone else runs one region and must not pay
for it.

## Decision

- Each namespace is authoritative in exactly one region, its home region.
  Writes are accepted only there; other regions answer `421 Misdirected
  Request` (later: forward the request to the home region).
- Replication follows Temporal's multi-cluster model: standby regions receive
  changes asynchronously; an administrator explicitly promotes a standby.
- Every namespace carries a `failover_version` that increases on each
  promotion. Every write carries a `namespace.Fence` with the version it read,
  and the store rejects the write (`ErrFenced`) if the version changed, in the
  same transaction (PostgreSQL: `FOR SHARE` on the namespace row; SQLite:
  `BEGIN IMMEDIATE`).
- Replication itself is not built yet. The fence exists from day one because
  retrofitting it onto every write path later is error-prone.

## Consequences

- Single-region installs pay one indexed read per write.
- A demoted region cannot keep accepting writes after a promotion, which
  prevents split brain.
- Moving a namespace between regions is an admin operation, never implicit.
