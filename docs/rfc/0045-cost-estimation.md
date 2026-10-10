---
rfc: 45
title: Cost estimation
status: draft
created: 2026-10-10
---

# RFC 45. Cost estimation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Most commercial TACOS show an Infracost estimate on each plan and can block on
a threshold (Spacelift, env zero, Scalr, Harness, Gruntwork, Stategraph,
OpenTaco, Terrakube).

## First ideas

- A runner job step (RFC 6) runs the estimator; the backend stores and shows
  the result next to the plan.
- Policy (RFC 13) can read the cost difference.

## Open questions

- Which estimator, and is it a recommended step or a built-in?

## Outcome

None yet.
