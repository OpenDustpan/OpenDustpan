---
rfc: 19
title: Users, teams and roles
status: draft
created: 2026-10-10
---

# RFC 19. Users, teams and roles

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

ADR 6 decides that OpenDustpan issues its own tokens and that SSO only signs
people in. It leaves users, teams and roles as "next identity work". Today one
static admin token exists. Every commercial product and both open-source TFE
alternatives have users, teams and a role set; Spacelift, Pulumi, Scalr and
env zero have custom roles. Competitors often charge for this.

## First ideas

- A small built-in role set over the tool-neutral actions of ADR 13, for
  example reader, planner (can preview, cannot apply), writer, admin.
- Custom roles as sets of actions. Scopes: namespace, the grouping of RFC 4,
  unit.
- Teams, with membership from the SSO provider's groups.
- A site admin view across namespaces (OTF has one).

## Open questions

- Which built-in roles, and is "planner" one of them (env zero has Planner and
  Deployer)?
- Can a role be granted on a label (RFC 4), as Spacelift grants on spaces and
  Pulumi on tags?
- How do roles map to the Pulumi CLI's and the TFE API's own team and
  permission endpoints?

## Outcome

None yet.
