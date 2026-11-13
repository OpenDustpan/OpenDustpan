## Context

Sign-in claim adapters and the SCIM adapter do not exist on main yet. This change lands the domain function, its tests, the sensitive-action plus fresh-check wiring for the configuration routes that exist, and contract tests for the mapping. Full SCIM and claim e2e runs after those adapters land.

## Decisions

1. **One membership function.** Add ApplyIdentityProviderMemberships in pkg/principal that takes principal, group IDs from the explicit single-mapping rule, source scim, claim, or ldap, and provider or SCIM token ID, and returns per-group accept or refused with sentinel ErrIdpAddRefused.
2. **Closed wins over trust and full sync.** Apply closed first: a closed group refuses even when trusted or full sync is on; else a protected group without trust and without full sync refuses; else accept and record source and provider or token ID. A repeat add of a present membership changes no row and writes no audit entry; a SCIM, claim, or ldap add that meets a local membership keeps the row and records the new source with one audit entry.
3. **One transaction with audit.** Check group flags, trust, and full sync inside the write transaction per request and write membership rows and audit entries in the same transaction; a SCIM batch evaluates each row against the same transaction snapshot, so a flag change during the batch applies to the next request and not to rows already evaluated.
4. **Alert channel with fresh check and lead time.** Raise each configuration and expiry alert as an audit entry plus a WARN line with the injected log/slog logger and injected clock; require server:manage-sign-in and a fresh authentication inside the one window on each trusted provider, claim mapping, and account matching change; specify only the same guard and the 168-hour SCIM token expiry alert for the SCIM token route for the scim change to apply; warn 168 hours before SCIM token expiry; a token with no expiry raises no warning and a re-armed expiry after an alert raises one more alert. Update the guide sentence named in guide-implies-protected-groups-refuse when enforcement ships.

## Structure

Unexported type membershipChange with constructor newMembershipChange that checks source, provider or token ID, and group flags; only the constructor can build a write, so a bypass is a compile error.

## Risks

- Adapters that bypass the one function reintroduce divergent behavior.
- WARN log lines are not a durable alert channel after events exist.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port checks closed first, then protected with trust and full sync, before any write and returns a sentinel error without changing state. | Send a claim that maps to a protected group, a closed-but-trusted group, and a closed group with full sync on and show each returns ErrIdpAddRefused with no membership row. |
| service | Service refuses in the write transaction and writes the audit entry in the same transaction; audit failure ends the change. | Remove the port check and show the service still refuses and still audits the refused add; fail the audit write and show no membership is added. |
| HTTP handler | HTTP adapter maps refusal to a SCIM error for SCIM and to skip-with-sign-in-allowed for claims, maps audit failure to 500, and declares server:manage-sign-in plus a fresh check on each configuration route. | Send a SCIM add to a protected group and a claim add to the same group and show SCIM error versus sign-in allowed with no membership; call each configuration route without a fresh step and with an expired fresh step outside the one window and show 403; fail the audit write on SCIM and on a configuration change and show 500 with no state change. |
