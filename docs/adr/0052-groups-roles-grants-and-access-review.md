# 52. Groups, roles, grants and the access-review export

Status: accepted · 2026-10-13. The built-in planner role added by ADR 212. Decisions 1 and 2 superseded in part by ADR 215: a principal and a group can belong to one namespace, by a setting. Decision 5 superseded in part by ADR 216: a setting decides whether a grant on the installation reaches a namespace. Decision 9 superseded by ADR 223

Changed by:

- ADR 67 changes decisions 1, 3 and 4: principals of a namespace.
- ADR 67 extends decision 10: the export lists who holds
  principal:manage-tokens.
- ADR 70 changes: the consequence on groups from an identity provider.
- ADR 203 extends decision 10: attachments with an invalid authority in the
  access-review export.
- ADR 212 extends decision 6: the built-in planner role.
- ADR 215 changes decisions 1 and 2: principals and groups can belong to one
  namespace.
- ADR 216 changes decision 5: a setting decides if installation grants reach a
  namespace.
- ADR 222 supersedes decision 9: attribute conditions on grants.
- ADR 223 supersedes decision 9: attribute conditions are access limits.
- ADR 223 extends decision 10: the access-review export lists the limits.
- ADR 354 extends decisions 1, 3 and 4: a fourth record kind, agent.
.
## Context

ADR 6 leaves users, teams and roles as "next identity work". Today one static
admin token exists, and the only `Authorizer` allows administrators.
[RFC 19](../rfc/0019-users-teams-roles.md) collects the questions. ADR 17
requires that each endpoint declares its actions and their objects. ADR 51
makes a collection an object of a grant.

The project owner answered on 2026-10-11, 2026-10-12 and 2026-10-13:

- About teams: "Teams are too narrow. In most businesses, \"teams\" are a
  mess. Users switch teams, they temporarily join projects on different
  teams, etc. So long as we assume that a user can be a member of more than
  one group we should be fine."
- About the built-in roles: copy the role sets of competitors, and prefer a
  set that is too large to a set that is "too narrow". RFC 19 quotes the
  full answer.
- About grants on a label: "Collections only". This replaces the "Yes." of
  2026-10-12.
- About the access-review export: a snapshot of the current grants, and "a
  historic audit export (between two timestamps)". It covers "every grant.
  But the export can allow scoping when it's generated." A principal with
  no sign-in and no token use shows "Never" as the last access time.
- About the scope of a group, on 2026-10-13: "Installation-wide".
- About custom roles, a view for site administrators, grants on the whole
  installation and members that are not persons, on 2026-10-13: the owner
  chose all four, and wrote: "Non-person group members is very specific. Any
  actor can be assigned any role on any object. Our RBAC/ABAC should be
  flexible to account for this."

## Decision

1. **Principals belong to the installation.** A person, the owner of a
   service token and a workload are principals of the whole installation,
   not of one namespace. A grant on each object decides what a principal can
   do there. This follows from the installation-wide groups of decision 2.
   An MFA reset (ADR 46) also acts on the principal in the whole
   installation.
2. **Groups, not teams.** A group is a named set of principals. A group
   belongs to the whole installation, not to one namespace. A principal can
   be a member of more than one group. A membership can start and end at any
   time. Where a tool protocol has its
   own word, such as a team in the TFE-compatible API, that API keeps its
   word.
3. **Any principal, any role, any object.** A grant names one holder, one
   role and one object. The holder can be any principal or a group: a
   person, the owner of a service token (ADR 53), a workload (ADR 54) or a
   group. The object can be the installation, a namespace, a collection
   (ADR 51) or a unit. A label never grants access (ADR 51).
4. **Any principal can be a member of a group.** A person, a service token
   owner and a workload can each be a member.
5. **A role is a set of tool-neutral actions** (ADR 13, ADR 17). A
   principal can do an action on an object when a grant on that object, or
   on an object that holds it, gives a role with that action. The grant can
   name the principal or a group of the principal. The installation holds
   its namespaces. A namespace holds its collections and units. A collection
   holds its units.
6. **A broad built-in role set.** The built-in roles follow the role sets of
   other products. When a choice is close, the set includes the extra role.
7. **Custom roles.** An administrator can make a role as a set of actions.
   A custom role works like a built-in role in a grant.
8. **A view for site administrators.** A principal with the right grant on
   the installation sees and manages every namespace in one view.
9. **The model can grow.** The project owner wrote "RBAC/ABAC". The
   authorization model should let a later change add conditions on attributes
   to a grant, without a change to the shape of decision 3.
10. **The access-review export.** The server answers an export of grants:
    - a snapshot of the grants at the time of the request.
    - a history of the grants between two timestamps that the caller gives.

    A caller asks for one of the two. The export covers every grant. The
    caller can limit it when it makes the export, for example to one
    namespace, collection, unit, principal or group.
11. **What a row holds.** Each row names the principal or the group, the
    role, the object and the time of the grant. It also holds the last
    sign-in or the last access token use of the principal. When the principal
    never signed in and never used an access token, the field shows `Never`.
    A history row also holds the time that the grant ended, if it ended.
12. **Formats.** The REST API answers the export in JSON. The console also
    offers it as CSV, made from the same API response (ADR 17).
13. **Its own action.** The export needs its own action, for example
    `namespace:read-access`. Each export can write an audit entry (ADR 41,
    RFC 22). The export helps with periodic access reviews (ISO/IEC
    27002:2022 control 5.18, SOC 2 CC6.2 and CC6.3).
14. **An endpoint answers the access of a caller.** One endpoint answers
    what a principal can do on one object, and which grants give it. The
    console uses this endpoint to show what a person can do. The console
    does not compute access in the browser (ADR 17).
15. **API.** Each operation on groups, memberships, roles and grants has a
    REST API and its own action (ADR 17), named in the style of ADR 13 and
    the actions that exist, such as `unit:read` and `namespace:read-audit`.
    Each change passes the policy check and writes an audit entry.

## Not decided here

[RFC 19](../rfc/0019-users-teams-roles.md) keeps these open:

- (research) The list of built-in roles, and how roles map to the team and
  permission endpoints of the Pulumi Cloud-compatible API and of the
  TFE-compatible API.
- Which administrators can make custom roles, and whether a custom role
  can belong to one namespace.
- Whether a group can be a member of another group.
- Which conditions on attributes a grant can hold, and when they come.
- The names of the actions of decision 15, and the action that the access
  endpoint of decision 14 needs. RFC 19 lists lead proposals.
- How the export shows a grant to a group. A group has no last access.

## Consequences

- The `Authorizer` port checks a principal, an action and an object, and
  finds the grants on the object and on the objects that hold it.
- The history export needs the start and the end of each grant and of each
  group membership. The server keeps them when a grant or a membership
  ends, not only the current rows.
- Groups, memberships, roles and grants need numbered migrations in each SQL
  dialect (CLAUDE.md).
- `GLOSSARY.md` gets the terms "group", "role" and "grant".
- The `Authorizer` takes the installation as an object, beside a namespace,
  a collection and a unit (ADR 17).
- Groups from an identity provider arrive through SCIM or a sign-in adapter
  (ADR 55).
