# 374. Namespace discovery from inside, and the window of the old event secret

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Extends ADR 52 decision 5: a grant on a collection or a unit also opens the
  discovery reads of its namespace.
- Extends ADR 271 decision 8: the window of the old event secret during a
  rotation.

## Context

Two OpenSpec changes stopped on a question that no ADR answered. The project
owner answered both questions on 14 November 2026 ("All approved").

- `groups-roles-grants`: a principal with a grant on a collection or on a
  unit only cannot use the OpenTofu CLI or the Pulumi CLI on that unit. The
  CLI first reads the organization of the namespace. OpenTofu reads
  `GET /api/v2/organizations/{org}/entitlement-set`, the organization and the
  workspace by organization. The Pulumi CLI calls the organization routes. These routes check an action on the namespace. ADR 52
  decision 5 gives an action on an object only through a grant on that object
  or on an object that holds it. A grant on a collection does not hold the
  namespace, so the CLI stops before it reaches the unit.
- `git-host-webhook-intake-post`: ADR 271 decision 8 keeps two active event
  secrets during a rotation, and an event is valid when it matches either one.
  It does not say how long the old secret stays valid.

## Decision

1. **Namespace discovery from inside (owner, 2026-11-14).** The organization
   and discovery routes of a namespace also answer a principal who holds a
   grant on a collection or on a unit in that namespace. These are the TFE
   routes `GET /api/v2/organizations/{org}/entitlement-set`, the organization
   read and the workspace reads by organization, and the organization routes
   of the Pulumi Cloud-compatible API that the Pulumi CLI calls.
   1. The answer holds only what the CLI needs: that the namespace exists,
      and its entitlements.
   2. A list route still filters its items to what the caller can read. A
      grant on one collection never shows a unit of another collection.
   3. Each other caller gets the same 404 as for an unknown namespace, with
      the same body. The answer is no existence oracle.
   4. The answer names the grant that allowed it, so that a person can see
      why the server answered.
   5. The check reads the grants at each request. A revoked grant loses the
      discovery read on the next request.
2. **The old event secret stays valid for at most 7 days (owner,
   2026-11-14).** During a rotation, the old event secret is valid for the
   check of an event only. It ends after 7 days, or earlier when the operator
   ends the rotation (the confirmation of the rotation). Each event that the server accepts with the old
   secret writes an audit entry. The API shows the effective end of the
   window. The server refuses an event signed with the old secret after the
   end, or after the confirmation. A secret older than the old secret is never
   valid: a registration keeps at most two event secrets.

## Consequences

- `groups-roles-grants` changes the TFE adapter, the Pulumi organization
  routes and the route declarations, with hostile tests for each case of
  decision 1.
- `git-host-webhook-intake-post` keeps two event secrets during a rotation,
  with the window of decision 2 and hostile tests for the end of the window,
  the confirmation and a third secret.
- The guides of collection grants and of Git host connections tell these
  rules.
