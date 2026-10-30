---
rfc: 108
title: Run options, re-runs and run variables
status: draft
created: 2026-10-30
---

# RFC 108. Run options, re-runs and run variables

Status: draft. A stub. It holds gaps from the competitor review of RFC 6
that ADR 282 moved here. It records the gaps and first ideas only.

## Problem

A job has four tool-neutral operations (ADR 100 decision 3). Other products
give more control over one run:

- Replace or target resources, and allow an empty apply (HCP Terraform,
  Spacelift).
- A retry with the same parameters, run-scoped variables and a switch for
  debug logs (Scalr).
- Standard run variables, such as the job ID and the commit, and a tool
  version pin for each unit (Burrito, HCP Terraform).

## Options

To be written. Include "do nothing".

## Proposal

None yet.

## Open questions

- Which options change what a plan shows, so that the approval of the plan
  must bind them (ADR 191)?
- Can a run-scoped variable hold a secret, and how does the log mask it?
- Is the tool version pin a field of the unit, of the profile or of the
  runner image?

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

None yet.
