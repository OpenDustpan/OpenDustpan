---
rfc: 25
title: Drift detection and remediation
status: draft
created: 2026-10-10
---

# RFC 25. Drift detection and remediation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Drift detection is the most common feature in the competitor review: HCP
Terraform, Pulumi Cloud, Spacelift, env zero, Scalr, Harness, Terramate, Atmos
Pro, Gruntwork, Firefly, Burrito, tofu-controller and the Pulumi Kubernetes
Operator all have it. OpenDustpan has only a motivation line in RFC 6.

## First ideas

- A scheduled refresh-only preview or plan per unit, run on runners (RFC 6),
  never in the backend.
- The result sets the unit's status in the inventory (RFC 26).
- Actions on a drift: ignore, accept the cloud (sync state), revert the cloud
  (apply), or open a pull request that changes the code (RFC 12).
- Spread schedules over a window and pause after repeated failures (Scalr);
  notify through RFC 23.
- Continuous validation of OpenTofu `check` blocks (HCP Terraform) fits the
  same schedule.

## Open questions

- Does showing which resources drifted need RFC 39 (reading state), or only
  the plan's summary?
- Is automatic remediation allowed, and under which edit policy (RFC 13)?

## Outcome

Accepted in part as [ADR 258](../adr/0258-drift-remediation-is-a-plan-that-a-person-approves.md)
on 2026-10-20: no automatic apply. A remediation is a job whose plan a
person approves. The schedule, the status and the pull request action stay
open.
