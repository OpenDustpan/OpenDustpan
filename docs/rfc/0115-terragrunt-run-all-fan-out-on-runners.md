---
rfc: 115
title: Terragrunt run-all fan-out on runners
status: draft
created: 2026-11-01
---

# RFC 115. Terragrunt run-all fan-out on runners

Status: draft. A stub. ADR 304 decides that, on OpenDustpan runners, a shim
turns `terragrunt run-all` into one job for each Terragrunt unit, under a
change order, started by a discovery job. This RFC holds the open points of
that shim. It is the design work of milestone 9.

## Problem

The shim must find the Terragrunt units, put them in a change order, and run
one job for each unit. ADR 304 does not say how.

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Unit discovery. How does the discovery job find the Terragrunt units, and
  how does it map each one to a unit of OpenDustpan? What does it do for a
  unit that has no workspace yet?
- Dependency order. Does the shim give ordered apply jobs, or does it use a
  change order that does not order its apply jobs (ADR 190)? Narrow claim
  only: Terragrunt gives an order, and a change order gives none today. This
  RFC decides what the shim does about that.
- A failure in one unit. What happens to the other units when one apply job
  fails? A change order moves to `partially-applied` (ADR 190 decision 7).
- Limits. What does the shim do for more than 100 units, or for units of
  more than one namespace (ADR 190 decisions 3 and 12)?

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
