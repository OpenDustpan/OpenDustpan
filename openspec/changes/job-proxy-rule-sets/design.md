## Context

Namespace and unit proxy rule sets are plain JSON data. The server stores each set, checks each proxy rule, joins the scopes of a unit, and shows a rendered view for debug only.

## Decisions

1. **Join scopes at read time.** Store one document per scope with digest. Join at read time in fixed order: namespace set then unit set. Each joined entry carries scope, operation, host, port, method, and canonical path match. Ids stay unique within each scope. Duplicate ids across scopes stay allowed and show as two entries with scope namespace and scope unit.
2. **Strict validation of each proxy rule.** Reject unknown fields, bad id form, duplicate id within one set, bad host, bad port, bad method, bad path pattern, and bad operation. Apply the port default of ADR 330 decision 4 when port is absent. Return the proxy rule id and the field in the error. Check canonical path only and keep no normalized copy. An empty scope means no proxy rules from that scope. A unit with no set in either scope has no proxy rules and keeps current behavior.
3. **Concurrency and rendered view.** Last write wins for one scope with no optimistic check in this part. Each save writes a new digest and an audit entry with old and new digest. The rendered view shows stored patterns without template render. Templates render when the job starts in a later part.

## Structure

Unexported type `proxyRuleSet` with constructor `newProxyRuleSet` that checks all invariants. Only the constructor makes a value. Stored references use stable namespace ID and unit ID, never the name.

## Risks

- A bad join order changes which proxy rule allows a host.
- A weak host or path check widens access past the intended host.
- A rendered view leaks secret values to a reader without need.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port checks shape and values: id form, id uniqueness within the set, operations set, host form, port range and default, method set, path pattern form with `*` and `**`, canonical path only. | Send a proxy rule with `..`, non canonical path, upper case host, bad port, duplicate id, and unknown operation directly to the port. Each case fails. |
| service | Service checks authorization actions, fresh authentication, scope join order, and digest write with old and new digest. | Save a unit set from a principal without `unit:manage-proxy-rules` and without fresh authentication. The save fails closed. |
| HTTP handler | Handler checks auth, content type, size limit, and operation query value on the rendered route. Rendered route needs read access on the unit. | Call the rendered route with a bad operation and with no read access. Each call fails with the fixed error. |
