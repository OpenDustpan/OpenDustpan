---
rfc: 43
title: Ephemeral units and time to live
status: draft
created: 2026-10-10
---

# RFC 43. Ephemeral units and time to live

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Temporary environments cost money when people forget them. env zero sets a
time to live with org and project maximums and warning emails; HCP Terraform
and Pulumi Cloud destroy after inactivity or at a date; Harness has it in
limited availability.

## First ideas

- A time to live on a unit, set at creation (also by a blueprint, RFC 14) and
  editable.
- Warnings before destroy through RFC 23; the destroy is a runner job (RFC 6).
- Unlike HCP Terraform, the destroy can require approval (RFC 13).

## Open questions

- Is the record deleted after the destroy, or kept (RFC 17's soft delete)?

## Outcome

None yet.
