## Context

SCIM is a provisioning adapter behind a port. It writes persons, groups, and memberships. It never issues sessions or tokens. Removal disables sessions and tokens at once.

## Decisions

1. **Service owns the writes.** One service owns SCIM writes. It creates or links the person by stable subject, creates groups by stable ID, and routes each membership write through the one membership function of one-membership-function-identity-provider in the same transaction. The SCIM adapter maps wire fields and does no store write.
2. **Disable on delete.** Removal is disable first. SCIM DELETE disables the person, disables sessions and access tokens, and keeps the rows. When the revocation setting from the generic settings API is on, the same transaction revokes them instead. The guard of keep-two-working-break-glass refuses DELETE of the last enabled break-glass principal with 409 and no state change.

## Structure

Untrusted SCIM input never reaches the store. Type scimRequest has unexported fields and only NewScimRequest makes it and checks the subject, group ID, and size.

## Risks

- A SCIM client mass-disables users; rate limit and audit each removal.
- Two clients race on the same subject; the subject has a unique index and one wins.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Bearer token with SCIM scope on each SCIM route, checked at the handler. Unknown or weak token gets 401. | Send SCIM create with no token, a wrong token, and a token without SCIM scope; each gets 401 and writes no row. |
| Service | Strict decode of SCIM JSON, field allowlist, size limit, and stable ID check in the service constructor. | Send extra fields, oversize body, and bad subject; each is refused and writes no row. |
| Port adapter | Store checks the namespace fence and the grant on the group object in the same transaction. | Call the service with a fence of another namespace and without the grant; each write is refused. |
