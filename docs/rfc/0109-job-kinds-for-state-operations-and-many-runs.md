---
rfc: 109
title: Job kinds for state operations and many runs
status: draft
created: 2026-10-30
---

# RFC 109. Job kinds for state operations and many runs

Status: draft. A stub. It holds gaps from the competitor review of RFC 6
that ADR 282 moved here. It records the gaps and first ideas only.

## Problem

- Atlantis runs `import`, `state rm` and `state mv` as jobs. OpenDustpan
  jobs have only `plan`, `apply`, `destroy` and `refresh` (ADR 100).
- `terragrunt run-all` changes many workspaces in one command. A job
  produces one Pulumi update or one OpenTofu run, so the command does not
  fit one job.

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Does a state operation as a job need a gate, as an apply does?
- Is `run-all` one job for each Terragrunt unit, or a job kind that
  produces many runs?

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
