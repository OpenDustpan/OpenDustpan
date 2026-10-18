---
rfc: 38
title: Upgrade checks
status: accepted
adr: 33
created: 2026-10-10
---

# RFC 38. Upgrade checks

Status: accepted (ADR 33) for milestone 3: the refusal of a newer schema, the
`upgrade check` command and the upgrade test. The project decides the usage
ledger for deprecated features at the first deprecation.

The rest was a stub opened from the competitor review in the research doc
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

Accepted in part as [ADR 33](../adr/0033-upgrade-checks.md) for
milestone 3: the refusal of a newer schema, the `upgrade check` command and
the upgrade test.

Closed on 2026-10-18. The usage ledger for deprecated features waits for a
trigger: the project decides it at the first deprecation of a feature (ADR
210, "Not decided here").
