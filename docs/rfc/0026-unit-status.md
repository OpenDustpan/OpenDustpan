---
rfc: 26
title: Unit status in the inventory
status: draft
created: 2026-10-10
---

# RFC 26. Unit status in the inventory

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

ADR 13's inventory has a status for each unit, with no defined values.
Competitors show a small vocabulary with counts: Terramate has healthy, failed
and drifted with a rule that failed wins; Harness has "apply needed"; HCP
Terraform and env zero lead with "needs attention". The console's first screen
depends on this list.

## First ideas

- A fixed, tool-neutral vocabulary, for example: in sync, running, waiting for
  approval, apply needed, drifted, failed, locked.
- A documented precedence when more than one applies.
- Each tool package maps its own states to it in the same transaction (ADR
  13), and the console keeps each tool's own words on its own pages.

## Open questions

- Is "locked" a status or a separate flag?
- Where does "apply needed" come from for a unit with no runner (RFC 12's
  "main is ahead of what is applied")?

## Outcome

None yet.
