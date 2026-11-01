# 65. Reading the installation audit log is an operator action

Status: accepted · 2026-10-14. The `server:` action names superseded in part by ADR 210. The operator or namespace scope of each installation action given by ADR 216

Changed by:

- ADR 210 changes: the route paths and the server: action names.
- ADR 216 changes: the operator or namespace scope of installation actions.

## Context

[ADR 60](0060-audit-log-port-and-log-sinks.md) decision 7 adds the action
`server:read-audit` on the object `@server`, and says "An administrator has
it". It does not say which administrator.

[RFC 19](../rfc/0019-users-teams-roles.md) puts each action in one of three
families. The operator family holds "settings, backups and restores,
maintenance, the instance registry, and the audit log of the installation
scope". The built-in role `installation-operator` holds the operator family
only, `account-administrator` holds administration and data, and
`site-administrator` holds all three.

The two texts can give different answers for `account-administrator`. The
code that reads `@server` must use one answer.

Accepted by the project lead under the owner's delegation of 2026-10-13.

## Decision

1. `server:read-audit` is in the operator family, as RFC 19 says. The roles
   `installation-operator` and `site-administrator` hold it.
   `account-administrator` does not hold it.
2. "An administrator" in ADR 60 decision 7 means a principal with a grant
   that holds `server:read-audit` on the installation. This ADR replaces
   that sentence. The rest of ADR 60 does not change.
3. `namespace:read-audit` on the installation does not give `server:read-audit`.
   A route whose object is `@server` asks only for `server:read-audit`. When
   such a route declares no server action, it refuses the request.

## Consequences

- An account administrator manages principals and grants, but cannot read
  the record of those changes in `@server`. A site administrator, or an
  operator, can. The person who changes access and the person who reviews
  the record of the change can be two different people.
- The catalog text of `installation-operator` names the installation audit
  log. It says that the role reads no namespace data.
- To let an account administrator read `@server`, give that person a second
  grant of `installation-operator`, or a custom role with
  `server:read-audit`. The grant appears in the access review.
