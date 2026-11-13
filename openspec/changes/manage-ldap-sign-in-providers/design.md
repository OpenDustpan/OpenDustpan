## Context

The static provider reads flags and files. Stored providers live in the database and share the LDAP adapter, the group applier, and the egress and TLS guards. The bind password is a reference to an environment variable or a file.

## Decisions

1. **Stored provider record.** A record holds URL, base DN, filters, group settings, limits and a password reference. The subject attribute is entryUUID or objectGUID only (Owner decision, 13 Nov 2026, queue P20). The static ID is reserved. The stable provider ID keys subjects and grants.
2. **TLS rule.** A plain URL to a non-loopback address is refused unless the plain-connection setting is on. A missing or not valid setting counts as off. Sign-in re-reads the setting.
3. **Group claims path.** The adapter returns the flat group list. Memberships change only through the one membership function with source ldap. It skips unknown groups, counts removals and records the source kind.
4. **Static binding.** The first start records ID and URL. A changed URL or a lost record refuses the start. The rebind route checks the new URL, needs a reason and its own action, revokes sessions and writes an audit entry.
5. **Uniqueness.** A unique database index on the directory URL makes a concurrent write lose with 409.
6. **Adapter limits.** The adapter keeps the user-name bounds, 10 second timeouts and the 1000-entry search cap.

## Structure

Unexported ldapProvider type with constructor newLDAPProvider that checks ID, URL, DN, filters, subject attribute and limits. Only the constructor makes a value. The password is a reference field with no plain value in the API model.

## Risks

- A group claim adds a member to a protected group.
- The bind password leaks through the API, logs or audit entries.
- A lost binding record lets a changed directory start without a check.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| API validation | Allowlist for URL scheme, DN, filters, attribute names, subject attribute and limits. | POST an ldap:// URL, a mail subject attribute and an oversize filter; each is refused before any connection. |
| service | Reserved ID check, rebind action, binding comparison, audit entry. | Create with the static ID, rebind without the action, delete the binding record and start. |
| database | Unique index on the directory URL. | Two parallel creates with one URL; one gets 409. |
| LDAP adapter | Egress, TLS, no referrals, no empty password, user-name bounds, limits, disabled user refused. | Use a blocked host, a referral answer, an empty password and a disabled user; each sign-in is refused and no password is logged. |
