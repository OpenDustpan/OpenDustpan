---
rfc: 18
title: State operations: history, restore, locks and leases
status: accepted
adr: 28
created: 2026-10-10
---

# RFC 18. State operations: history, restore, locks and leases

Status: accepted (ADR 28) for milestone 3: history, restore and unlock, for
both tools. Retention, content diff and the manual lock stay open.

The rest stays a draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

A state backend's first recovery tools are version history and unlock. HCP
Terraform shows each state version with a diff and a rollback that needs a
lock. OpenTaco has `taco unit versions` and `restore`. Harness writes a
rollback as a new version. env zero has Force Release for stuck locks and a
manual lock with a reason. Atlantis shows who holds each lock. OpenDustpan
keeps only the latest Pulumi checkpoint today and has no unlock action.

## First ideas

- History for both tools: list, download or export a version, diff between
  versions (by resource count or address, see RFC 39), retention.
- Restore always writes the old state as a new version. It never changes
  infrastructure, and the console says so.
- Locks and leases: who holds it, since when, from which run or commit;
  force-unlock; cancel a stuck Pulumi update; a manual lock with a reason that
  pauses jobs.
- Every one of these actions goes to the audit log (RFC 22).

## Open questions

- Who may force-unlock: an admin only, or anyone with write on the unit?
- Which retention policy is the default: keep all, keep N, or keep N days?
- Does a diff between versions need RFC 39 (reading state), or can it compare
  only sizes and serials?

## Outcome

Accepted in part as [ADR 28](../adr/0028-state-operations.md) for
milestone 3: history, restore and unlock, for both tools. Retention, content
diff and the manual lock stay open for a later decision.
