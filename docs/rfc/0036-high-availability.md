---
rfc: 36
title: High availability and scale-out
status: draft
created: 2026-10-10
---

# RFC 36. High availability and scale-out

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Terraform Enterprise has an active-active mode. ADR 4 gives OpenDustpan home
regions and fencing, but replication is not built, and RFC 7 proposes roles
but no HA design.

## First ideas

- Several stateless API instances over one PostgreSQL database and one object
  store.
- Leader election or database locks for background work (schedules, retention,
  drift).
- Asynchronous replication to standby regions, as ADR 4 plans.

## Open questions

- Which background jobs need exactly one runner, and how is that guaranteed?

## Outcome

None yet.
