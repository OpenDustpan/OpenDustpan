---
rfc: 19
title: Users, teams and roles
status: discussing
created: 2026-10-10
---

# RFC 19. Users, teams and roles

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the access-review export below on 2026-10-11.

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
- An access-review export of who holds which role on which namespace or unit
  (see Proposal).

## Proposal

### Access-review export

The project owner chose this export. It helps a reviewer check access.

- The server offers an export of access grants.
- Each row names a principal, a role, a namespace or a unit, the date of the
  grant, and the last sign-in or last access token use of the principal.
- The REST API answers the export in JSON.
- The console also offers the export in CSV for spreadsheets.
- The export needs its own action, for example `namespace:read-access`.
- The export follows ADR 13, ADR 17 and ADR 41. Each request declares its
  action. Each read may write an audit entry.
- The export helps with periodic access reviews. It helps with ISO/IEC
  27002:2022 5.18, SOC 2 CC6.2 and CC6.3.

## Open questions

- Which built-in roles, and is "planner" one of them (env zero has Planner and
  Deployer)?
- Can a role be granted on a label (RFC 4), as Spacelift grants on spaces and
  Pulumi on tags?
- How do roles map to the Pulumi CLI's and the TFE API's own team and
  permission endpoints?
- Does the export list only current grants, or past grants as well?
- Does the export cover grants on every object, or only on namespaces and
  units?
- How does the export show a principal that never signed in or never used an
  access token?

## Owner direction (2026-10-11)

The project owner chose an access-review export as the next step of this RFC.
The export lists who holds which role on which namespace or unit. Each row
holds the date of the grant and the last sign-in or last access token use of
the principal. The REST API answers JSON. The console also offers CSV for
spreadsheets. The export needs its own action, for example
`namespace:read-access`. Each read may write an audit entry (ADR 41). It helps
with periodic access reviews (ISO/IEC 27002:2022 5.18, SOC 2 CC6.2 and CC6.3).
The related ADRs are ADR 6, ADR 13, ADR 17 and ADR 41.

## Outcome

None yet.
