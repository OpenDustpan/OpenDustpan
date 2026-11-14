## Context

Delegation is a stored record that names the agent, the person, the scope and the time. This step creates the agent principal, then writes, reads, lists and revokes the row. It starts no run and enforces no tool call.

## Decisions

1. **Stored delegation row.** Table delegation holds one row per delegation: id with prefix dlg-, holder stable ID of the NEW agent principal dusty, creator subject of the person, scope namespace ID plus level look, suggest or operate, created time in Unix milliseconds, expiry time at most 30 days after creation, revoked time or null. Scope stores the stable namespace ID, never the name. Holder names the agent, creator names the person.
2. **Agent principal and who may create, read, list and revoke.** The NEW agent principal dusty of the NEW kind agent exists before any delegation. The server makes it when an administrator connects a model, with stable ID dusty. It holds no credential except one bound to a live delegation. Create needs namespace read on the scope namespace. Revoke of own delegation needs the creator with a live credential and no extra fresh step. Revoke of another row needs namespace manage on the scope namespace or installation manage. Read of one row needs creator or namespace manage on the scope. Namespace list needs namespace manage on that namespace. Writes run in a fenced transaction in the scope namespace.
3. **Lookup, leak, fresh-auth and lifecycle rule.** Unknown id answers 404 with the NEW error code delegation.not-found and repeats no input. Read of a row by a caller who is neither creator nor administrator answers 404 with the same code. Second revoke of a revoked row answers success with no state change. Revoke needs a live session or a live token only, the same rule for every revoke caller, to keep emergency revoke fast. Deleted namespace keeps its rows unreadable through the namespace list; direct read by the creator still answers the row with live state expired or revoked.

## Structure

Unexported type delegation with constructor NewDelegation that checks holder is dusty, creator is a person subject, scope namespace id, level and expiry; only the constructor makes a live value.

## Risks

- Reason text is data and never an instruction to the agent loop.
- A delegation row carries no secret and no token.
- Creation uses the live credential of the person with no extra fresh step in this step; short expiry, list, revoke and audit bound the theft window.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | Handler allowlist for level, scope namespace id format, expiry bound of 30 days, holder equal to dusty, creator equal to caller, and fenced write. | Send level admin, unknown namespace id and expiry 31 days out; each gets 422 and stores no row. |
| service | Service checks caller right for each route, creator equals caller on create, and live state on read. | Create for another holder and read of another creator row as a plain member; both get 403 or 404 with no row written or shown. |
| store | Store predicate checks expiry bound and holder equal to dusty in the same transaction as the insert. | Insert with expiry 31 days out directly at the store; the store refuses with the NEW error code delegation.invalid-expiry. |
