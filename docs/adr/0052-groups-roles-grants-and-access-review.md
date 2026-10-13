# 52. Groups, roles, grants and the access-review export

Status: accepted · 2026-10-13

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

## Decision

1. **Groups, not teams.** A group is a named set of principals. A principal can be a member of more than one group. A
   membership can start and end at any time. Where a tool protocol has its
   own word, such as a team in the TFE-compatible API, that API keeps its
   word.
2. **A grant gives a role on an object.** A grant names a principal or a
   group, a role and one object: a namespace, a collection (ADR 51) or a
   unit. A label never grants access (ADR 51).
3. **A role is a set of tool-neutral actions** (ADR 13, ADR 17). A
   principal can do an action on an object when a grant on that object, or
   on an object that holds it, gives a role with that action. A namespace
   holds its collections and units. A collection holds its units.
4. **A broad built-in role set.** The built-in roles follow the role sets of
   other products. When a choice is close, the set includes the extra role.
5. **The access-review export.** The server answers an export of grants:
   - a snapshot of the grants at the time of the request.
   - a history of the grants between two timestamps that the caller gives.

   A caller asks for one of the two. The export covers every grant. The caller can limit it when it makes the
   export, for example to one namespace, collection, unit, principal or
   group.
6. **What a row holds.** Each row names the principal or the group, the
   role, the object and the time of the grant. It also holds the last
   sign-in or the last access token use of the principal. When the principal
   never signed in and never used an access token, the field shows `Never`.
   A history row also holds the time that the grant ended, if it ended.
7. **Formats.** The REST API answers the export in JSON. The console also
   offers it as CSV, made from the same API response (ADR 17).
8. **Its own action.** The export needs its own action, for example
   `namespace:read-access`. Each export can write an audit entry (ADR 41,
   RFC 22). The export helps with periodic access reviews (ISO/IEC
   27002:2022 control 5.18, SOC 2 CC6.2 and CC6.3).
9. **API.** Each operation on groups, memberships, roles and grants has a
   REST API and an action (ADR 17). Each change passes the policy check and
   writes an audit entry.

## Not decided here

[RFC 19](../rfc/0019-users-teams-roles.md) keeps these open:

- (research) The list of built-in roles, and how roles map to the team and
  permission endpoints of the Pulumi Cloud-compatible API and of the
  TFE-compatible API.
- Custom roles, as sets of actions that an administrator makes.
- A view for site administrators across namespaces, and grants on the
  whole installation.
- Whether a group belongs to one namespace or to the whole installation.
- Whether a principal that is not a person (ADR 53, ADR 54) can be a member
  of a group.

## Consequences

- The `Authorizer` port checks a principal, an action and an object, and
  finds the grants on the object and on the objects that hold it.
- The history export needs the start and the end of each grant and of each
  group membership. The server keeps them when a grant or a membership
  ends, not only the current rows.
- Groups, memberships, roles and grants need numbered migrations in each SQL
  dialect (CLAUDE.md).
- `GLOSSARY.md` gets the terms "group", "role" and "grant".
- Groups from an identity provider arrive through SCIM or a sign-in adapter
  (ADR 55).
