---
rfc: 50
title: Policy authoring, testing and compliance packs
status: draft
created: 2026-10-10
---

# RFC 50. Policy authoring, testing and compliance packs

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Policies are hard to write. Spacelift captures real policy inputs and
simulates a changed policy on them; Harness shows the input with each
evaluation; Scalr runs a policy change against every workspace as a check;
Pulumi and HCP Terraform ship compliance packs (CIS, NIST, PCI).

## First ideas

- Captured samples and a simulator in the console.
- The input shown with every decision.
- Impact analysis of a policy change across all units, without blocking.
- Packs of ready policies, once RFC 13 picks an engine.

## Open questions

- Which of these depend on the engine choice in RFC 13?

## Outcome

Part accepted on 2026-10-18 by ADR 229: one decision record shows the facts
of each decision, the simulate route replays draft gates over stored
records, `dustpan gates test` runs test files with no server, and starter
packs are designed against named control frameworks and install in
`evaluate` mode. The rest stays open.
