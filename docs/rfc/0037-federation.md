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

None. The owner answered the one question. See "Owner direction (2026-10-12)".

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "Is there a real need, beyond regions in one install?": "Yes. We can
  proxy traffic between instances."

## Outcome

None yet.
