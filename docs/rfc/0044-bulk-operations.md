---
rfc: 44
title: Bulk operations
status: draft
created: 2026-10-10
---

# RFC 44. Bulk operations

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

env zero has nine bulk operations across environments; Scalr bulk-cancels and
approves runs; Burrito bulk-syncs layers.

## First ideas

- Select units by a saved view (RFC 40) and run one action on all: deploy,
  cancel, approve, lock, unlock, tag.
- Each action goes through the same access and edit policy as one unit, and
  the audit log records each.

## Open questions

- How are partial failures shown?

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 4 on 2026-10-18: a bulk call takes up to 100 IDs, checks, does,
records and audits each item on its own, is not atomic, and answers one
result for each ID. The first bulk operations stay open.
