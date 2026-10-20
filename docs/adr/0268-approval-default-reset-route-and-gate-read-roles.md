# 268. Approval-default reset route and gate read roles

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Amends ADR 230 decision 3

## Context

ADR 230 decision 3 lists the routes that set and read the approval default of
a scope. It lists `GET` and `PUT` for the approval default, and it lists
`GET`, `PUT` and `DELETE` for each approval rule. It names the read action and
the manage action of each route, but it does not say how an administrator
removes an approval default. It also does not say which roles of a namespace
hold the read action of the gates.

The first routes for approval defaults and approval rules need both answers.
An approval default is one setting of a scope. A namespace has no approval
rule until an administrator opts in. So an administrator must be able to go
back to the state of "no default rule".

A person whom an approval rule stops must be able to see which rule stopped
them. Without the read action, that person sees a refusal and no reason.

## Decision

1. **`DELETE {scope}/approval-default` resets the scope to no default
   rule.** The route is idempotent and returns 204, also when the scope has
   no default. It declares the same action as `PUT {scope}/approval-default`.
   It needs fresh authentication like the other writes of approval rules. An
   access token never gives fresh authentication. An approval default is a
   singleton setting, and approval rules are opt-in. So an administrator
   needs a way to remove the default without a sentinel value.
2. **The namespace roles viewer, auditor, writer and maintainer hold
   `namespace:read-gates`.** Of the namespace roles, only the administrator
   holds `namespace:manage-gates`. A person whom a gate stops must be able to
   see why. A namespace that wants a narrower read right makes a custom role,
   and a custom role is the way to give the manage action to a person who is
   not an administrator.

Benchmark: singleton settings have a matching reset in Google and AWS APIs.
Readers of a repository can see its branch rules on GitHub. No ADR claims
compliance.

## Consequences

- The route table in decision 3 of ADR 230 gets the row
  `DELETE {scope}/approval-default`, with the action of the matching `PUT`.
  A client reads decision 3 together with this ADR.
- A `DELETE` of an approval default that no one set is not an error. A client
  can call it again after a lost answer.
- Four more namespace roles can read approval rules and the approval default
  of their namespace. They cannot change them.
- The administrator of a namespace, the account administrator and the site
  administrator can still set and remove approval rules and defaults.
- ADR 230 changes only in its status line, which names this ADR.
