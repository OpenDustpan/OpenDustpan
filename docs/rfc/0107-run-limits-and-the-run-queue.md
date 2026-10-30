---
rfc: 107
title: Run limits and the run queue
status: draft
created: 2026-10-30
---

# RFC 107. Run limits and the run queue

Status: draft. A stub. It holds gaps from the competitor review of RFC 6
that ADR 282 moved here. It records the gaps and first ideas only.

## Problem

The job model has step timeouts and an expiry for queued and dispatched
jobs (ADR 100, ADR 101). Other products also have:

- A stop after a time with no log output (Spacelift, Pulumi).
- Run priority (Spacelift, Harness).
- Serialization of jobs by a key, so that two jobs with the same key never
  run at the same time (Spacelift, Harness, Pulumi).

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Is the time with no output a setting of the unit, of the pool or of the
  installation?
- Does a priority change the order of claims in one pool only, or across
  pools?
- Is a serialization key more than the unit, which the job model already
  serializes (wait reason `unit-busy`)?

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
