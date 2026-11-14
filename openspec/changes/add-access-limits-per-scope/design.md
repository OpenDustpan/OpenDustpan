## Context

Grants gain optional expiry; per-scope Cedar forbid limits sit after the grant check in the authorizer.

## Decisions

1. **Expiry as nullable timestamp.** Store expiresAt as Unix milliseconds nullable on the grant row; the authorizer treats null as no expiry and past time as no grant. A sweep marks past rows expired.
2. **Limits as stored forbid policies.** Store one Cedar forbid policy per limit with stable ID, scope, and policy text; type-check at save against the access schema.

## Structure

Unexported grantChecker built only by NewGrantChecker with now function and store; unexported limitSet built only by ParseLimitSet which type-checks forbid-only policies.

## Risks

- Expired grants linger in listings unless the review marks them expired.
- A broad forbid with no principal condition refuses every caller in scope.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service validation | Save validation refuses effect permit and out-of-scope gate lockout patterns. | Save a permit policy and a namespace limit that matches installation:manage-gates; both get gate.ErrInvalid. |
| authorizer check | Authorizer checks grants, then evaluates in-scope forbid limits in one Cedar request; expired grants give no allow. | Call with an expired grant and with a matching collection forbid; each gets refusal even when a grant allows. |
| HTTP route | Each mutating route declares its scope action and needs fresh authentication; each change writes an audit entry. | Call set and delete routes without the scope action and without fresh step; each gets 403 and no write. |
