---
rfc: 55
title: Environment promotion and comparison
status: draft
created: 2026-10-10
---

# RFC 55. Environment promotion and comparison

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Massdriver compares two environments component by component and forks
environments; preview environments per pull request are common.

## First ideas

- Compare the config of two units made from the same blueprint (RFC 14) or
  program.
- Promote config from one unit to another as an edit (RFC 12).

## Open questions

- Does this depend on RFC 4's grouping of units into environments?

## Outcome

None yet.
