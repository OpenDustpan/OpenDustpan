---
rfc: 109
title: Job kinds for state operations and many runs
status: draft
created: 2026-10-30
---

# RFC 109. Job kinds for state operations and many runs

Status: draft. A stub. It holds gaps from the competitor review of RFC 6
that ADR 282 moved here. It records the gaps and first ideas only.

Changed by:

- [ADR 304](../adr/0304-native-run-all-fan-out-applies-in-milestone-9.md)
  answers the `terragrunt run-all` question. It is one job for each
  Terragrunt unit, under a change order, with no new job kind. The
  state-operation questions stay open.

## Problem

- Atlantis runs `import`, `state rm` and `state mv` as jobs. OpenDustpan
  jobs have only `plan`, `apply`, `destroy` and `refresh` (ADR 100).
- `terragrunt run-all` changes many workspaces in one command. A job
  produces one Pulumi update or one OpenTofu run, so the command does not
  fit one job. ADR 304 answers this gap: a shim on runners makes one job for
  each Terragrunt unit, under a change order.

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Does a state operation as a job need a gate, as an apply does?
- ~~Is `run-all` one job for each Terragrunt unit, or a job kind that
  produces many runs?~~ Answered by ADR 304: one job for each Terragrunt
  unit, under a change order. The open points of the shim (unit discovery,
  dependency order, a failure in one unit) go to [RFC 115](0115-terragrunt-run-all-fan-out-on-runners.md).

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
