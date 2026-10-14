---
rfc: 19
title: Users, teams and roles
status: discussing
created: 2026-10-10
adr: 52
---

# RFC 19. Users, teams and roles

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the access-review export below on 2026-10-11.

Groups, grants, the principle of the built-in role set and the access-review
export are accepted as ADR 52 on 2026-10-13. The rest stays open.

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

- (research) How do roles map to the Pulumi CLI's and the TFE API's own team and
  permission endpoints?
- (research) Which built-in roles, by name and by action? The owner chose to
  copy the role sets of competitors (ADR 52, decision 4). The Outcome
  records the set.
- Which administrators can make custom roles, and can a custom role belong
  to one namespace?
- Can a group be a member of another group?
- Which conditions on attributes can a grant hold, and when do they come?

These come from the console UI spike (OpenDustpan/console PR #29). Each
"lead proposal" is a proposed default from the project lead for the owner to
decide. It is not a decision.

- How does the access-review export show a grant to a group? A group has no
  last access. Lead proposal: one row for each effective grant of each
  principal. A grant to a group gives one row for each member, with a
  column "via group <name>".
- Which actions does ADR 52 need? Lead proposal, in the style of ADR 13 and
  of the actions that exist (`unit:read`, `namespace:read-audit`,
  `server:backup`):

  | Action | Object | Operation |
  | --- | --- | --- |
  | `server:manage-principals` | installation | create, change and remove principals |
  | `group:read` | group | read a group and its members |
  | `group:manage` | group | create, rename and delete a group |
  | `group:manage-members` | group | add and remove members |
  | `role:read` | role | read a role and its actions |
  | `role:manage` | role | create, change and delete a custom role |
  | `server:manage-access` | installation | grant and revoke roles on the installation |
  | `namespace:manage-access` | namespace | grant and revoke roles on a namespace |
  | `collection:manage-access` | collection | grant and revoke roles on a collection |
  | `unit:manage-access` | unit | grant and revoke roles on a unit |
  | `server:read-access` | installation | export the grants of the whole installation |
  | `namespace:read-access` | namespace | export the grants of a namespace |

  The spike also proposed `unit:plan` (a preview or a plan) and
  `namespace:manage-tokens`. Lead proposal: `unit:plan` goes in the list of
  built-in roles (the "planner" question above), and the token actions go
  in RFC 20.
- Which action does the access endpoint of ADR 52 need? Lead proposal: a
  caller can ask about its own access with the read action of the object.
  To ask about another principal, it needs the `read-access` action of the
  object.

## Owner direction (2026-10-11)

The project owner chose an access-review export as the next step of this RFC.
The export lists who holds which role on which namespace or unit. Each row
holds the date of the grant and the last sign-in or last access token use of
the principal. The REST API answers JSON. The console also offers CSV for
spreadsheets. The export needs its own action, for example
`namespace:read-access`. Each read may write an audit entry (ADR 41). It helps
with periodic access reviews (ISO/IEC 27002:2022 5.18, SOC 2 CC6.2 and CC6.3).
The related ADRs are ADR 6, ADR 13, ADR 17 and ADR 41.

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "Which built-in roles, and is \"planner\" one of them": "Copy-pasta from
  the competitors here, rather too comprehensive than too narrow."
- Question "Can a role be granted on a label (RFC 4)": "Yes."
- Question "How do roles map to the Pulumi CLI's and the TFE API's own team and
  permission endpoints?": "No idea yet. Research and/or spike." The question
  stays in "Open questions".
- Question "Does the export list only current grants, or past grants as well?":
  "Cool idea. It would be cool to do both a historic audit export (between two
  timestamps), and a snapshot of the current."
- Question "Does the export cover grants on every object, or only on namespaces
  and units?": "every grant. But the export can allow scoping when it's
  generated."
- Question "How does the export show a principal that never signed in or never
  used an access token?": "It shows the last accessed time as \"Never\"?" The
  owner wrote this with a question mark.
- Comment, with no question named: "Teams are too narrow. In most businesses,
  \"teams\" are a mess. Users switch teams, they temporarily join projects on
  different teams, etc. So long as we assume that a user can be a member of
  more than one group we should be fine."

## Owner direction (2026-10-13)

The project owner answered a question of this RFC and of RFC 4 on
2026-10-13. Words in quotation marks are the words of the owner.

- Question "Can a role be granted on a label?": "Collections only". Grants go
  on a namespace, a collection or a unit. Labels are for views and filters,
  and they never grant access.

This answer replaces the owner's answer "Yes." of 2026-10-12 to the question
"Can a role be granted on a label (RFC 4)". A role cannot be granted on a
label.

Second round, through the project lead, on 2026-10-13:

- Question "Does a group belong to one namespace or to the whole
  installation?": "Installation-wide".
- Questions "Custom roles?", "A site-admin view and grants on the whole
  installation?" and "Can a non-person principal be in a group?": the owner
  chose custom roles, a site-admin view, grants on the whole installation and
  group members that are not persons. The owner added: "Non-person group
  members is very specific. Any actor can be assigned any role on any object.
  Our RBAC/ABAC should be flexible to account for this." So any principal (a
  person, a service, a workload or a group) can get any role on any object:
  the installation, a namespace, a collection or a unit.

## Outcome

Accepted in part as ADR 52 on 2026-10-13:

- Groups, not teams. A group belongs to the whole installation. A principal
  can be a member of more than one group, and any principal can be a member.
- A grant gives any role to any principal or group on any object: the
  installation, a namespace, a collection (ADR 51) or a unit. A label never
  grants access.
- A broad built-in role set that follows the role sets of other products,
  custom roles, and a view for site administrators.
- The access-review export: a snapshot of the current grants, or a history
  between two timestamps. It covers every grant, with scoping. It shows
  `Never` for a principal with no sign-in and no access token use. JSON from
  the REST API, CSV in the console, and its own action.

Lead decisions under owner delegation, 2026-10-13:

- Action names. `server:read-principals`, `server:manage-principals` and
  `server:manage-groups` act on the installation and replace the `group:*`
  proposal: ADR 52 does not make a group the object of a grant.
- History. A removal ends a membership and keeps its row. A delete ends a
  principal or a group and keeps its row. The access-review history reads
  these rows.
- Fail-closed audit. Each change writes its audit entry in the transaction
  of the change. If the entry fails, the change fails with 500. A failure
  entry for a refused change is written after it, as well as possible.
- No disabled flag yet. It comes back with the sign-in and token change that
  enforces it.
- Paging. Each list takes a cursor, a limit (default 100, maximum 1000) and
  a prefix, and the database answers it with an index. Each list answers
  `total` and `nextCursor`. A principal answers `groupCount`, and a group
  answers `memberCount`.
- Source. Each principal and group has a `source`: `local` today, `ldap` or
  `scim` with directory sync (ADR 55). A group whose source is not `local`
  refuses a membership change through the API with 409.
- Containment. A grant applies to its object and to each object that the
  object holds, along the fixed chain installation > namespace > collection >
  unit. Groups do not nest, and roles do not hold roles.
- Create a unit. A new unit needs `collection:create-unit` on its target
  collection. A placement rule only routes a unit. It never authorizes.
- Three families of actions. No action is in two families:
  - operator: settings, backups and restores, maintenance, the instance
    registry, and the audit log of the installation scope.
  - administration: principals, groups, memberships, roles, grants on the
    installation, and namespace create (`server:create-namespace`). The
    creator of a namespace gets the `admin` role on it in the same
    transaction.
  - data: every action inside a namespace, `namespace:admin` and the
    manage-access actions included.
- Three built-in roles on the installation: `installation-operator` (the
  operator family only), `account-administrator` (administration and data)
  and `site-administrator` (all three families).
- Data roles. The lead accepted this set on 2026-10-14, by the delegation of
  the owner: `viewer`, `auditor`, `secret-viewer`, `writer`, `maintainer` and
  `admin`. `docs/research/m4-built-in-roles.md` gives the facts behind it.
  `pkg/access/roles.go` holds the actions of each role. A planner role waits
  for a route that separates a plan from an apply.
- Grant history. A revoke ends a grant and keeps its row, and the delete of
  a custom role ends the role. A delete of a principal or a group revokes
  its grants in the same transaction. Each grant and role change writes its
  audit entry in the transaction of the change.
- No escalation through a role, a membership or a revoke. The lead decided
  these on 2026-10-14, by the delegation of the owner, after the security
  review of the roles and grants change:
  - A custom role holds only actions that its maker holds on the
    installation, on create and on change. A role that a current grant gives
    below the installation keeps data actions only.
  - A membership change is a grant by proxy. To add or remove a member, the
    caller must hold, on each object, every action that the current grants
    of the group give there.
  - A revoke, and the delete of a principal or a group, need every action
    that the grants give on their objects. The delete of a principal also
    ends its memberships, so the grants of its groups count too. Two admins of one namespace hold
    the same actions there, so each can revoke the other, the grant of the
    creator included. This is accepted.
  - A grant on a unit follows the stable ID of the unit (ADR 19), not its
    name. A new unit with the name of a deleted one does not get its grants.
    A deleted unit keeps its grants, and a restore brings them back.
  - A grant reads its subject and its custom role in the transaction of the
    grant, so a delete that commits first wins.
  - The TFE-compatible API answers the same 404 for an object that does not
    exist and for an object that the caller cannot read.
- Decisions at scale. One decision reads the groups of the caller in one
  query, the grants on the chain of the object in one query, and the custom
  roles of those grants in one query. A request reads the groups and the
  roles once. No cache lives across requests.

The other questions in "Open questions" have no outcome yet.
