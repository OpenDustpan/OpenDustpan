---
rfc: 110
title: "Step results: hooks, approval steps and scanners"
status: draft
created: 2026-10-30
---

# RFC 110. Step results: hooks, approval steps and scanners

Status: draft. A stub. It holds gaps from the competitor review of RFC 6
that ADR 282 moved here. It records the gaps and first ideas only.

## Problem

A job is a list of steps (ADR 101). Other products do more with the
result of a step:

- Hooks that run on success and on failure (env zero).
- A step that asks for an approval (env zero).
- Scanner results, for example from Checkov or TFLint, shown with the run
  and not only in the log (Scalr, Harness).
- A mask that a step asks for at run time, such as `::add-mask`
  (Spacelift). Today the runner masks only the secrets of the profile
  (ADR 101, ADR 102).

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Is an approval step a gate of ADR 103, or a new kind of wait?
- Which format do scanner results use, for example SARIF?
- Can a step that asks for a mask also remove a value from a log chunk
  that the runner already sent?

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
