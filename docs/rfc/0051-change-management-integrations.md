---
rfc: 51
title: Change management and portal integrations
status: draft
created: 2026-10-10
---

# RFC 51. Change management and portal integrations

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Regulated teams route changes through ServiceNow; platform teams expose
infrastructure in developer portals such as Backstage. Spacelift and Firefly
integrate with ServiceNow; Harness has its own portal; Pulumi has a Backstage
plugin.

## First ideas

- Change requests in an ITSM tool, opened and closed by approval events (RFC
  13, RFC 23).
- A Backstage plugin that reads the inventory and starts blueprints (RFC 14).

## Open questions

- Are these first-party adapters or community projects on the public API?

## Outcome

In part: [ADR 247](../adr/0247-works-with-backstage-an-entity-tab-of-units.md) accepts the Backstage plugin that reads the inventory.
