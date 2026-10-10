---
rfc: 37
title: One view over many OpenDustpan installs
status: draft
created: 2026-10-10
---

# RFC 37. One view over many OpenDustpan installs

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Upbound's console connects many control planes into one view. Large
organizations may run several OpenDustpan installs, for example one per
business unit or per network zone. ADR 4 covers regions of one install only.

## First ideas

- A read-only federated inventory across installs.
- Or: one install with many namespaces is always the answer, and federation is
  not needed.

## Open questions

- Is there a real need, beyond regions in one install?

## Outcome

None yet.
