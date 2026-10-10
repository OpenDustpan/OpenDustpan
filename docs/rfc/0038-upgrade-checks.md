---
rfc: 38
title: Upgrade checks
status: draft
created: 2026-10-10
---

# RFC 38. Upgrade checks

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Crossplane has a pre-upgrade check that finds removed or deprecated features
in use. OpenDustpan's APIs are contracts that deprecate before they remove;
operators need to know what an upgrade will break.

## First ideas

- A command that reports deprecated API use and configuration seen since the
  last upgrade, before the operator upgrades.

## Open questions

- Is this a server command, or a report in the console?

## Outcome

None yet.
