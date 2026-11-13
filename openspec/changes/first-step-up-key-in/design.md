## Context

Owner decision, 13 Nov 2026 (queue P17): an SSO installation gets its first step-up key from a host command with a one-time marker, in modes 1 and 2.

## Decisions

1. **One-time marker and one transaction.** The store method checks the marker and the key count, writes the key, sets the marker and writes the audit entry in one transaction. The API reads the marker under an installation read action.
2. **Email after commit.** The audit entry says the email is due. The email follows the commit. A failed send is logged with no driver text.
3. **Challenge use.** The store marks a challenge used with one conditional update, so concurrent attempts give one winner.
4. **Admin reset.** The store method clears the keys of a person and writes an audit entry. It covers a sole suspended key. The route and the action are those of the mfa change, not new ones.

## Structure

An unexported bootstrap token type with one constructor that checks mode, migration state, off-switch value and reason text. Only a value from it reaches the store method, which checks the marker.

## Risks

- A second bootstrap makes a backdoor key.
- A replayed challenge enrolls an attack key.
- A suspended sole key locks out the administrator.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| host command | Checks mode, pending migration, off-switch value and reason text. | Run in mode none, with a pending migration, with an unknown switch value and with a direction override in the reason; each refuses with no key. |
| service | Takes the lock and consumes the challenge once. | Replay a challenge and exceed the limit; both refuse. |
| store | Checks the marker and the key count in the same transaction. | Race two bootstraps; one key exists. |
