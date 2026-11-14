## Context

Token authentication is security code. Enforcement must fail closed at every layer so an expired, revoked, or idle token never authenticates even if one layer is bypassed.

## Decisions

1. **One authenticator checks every request.** Enforcement lives in the token authenticator, not in each CLI adapter. The Pulumi, TFE-compatible, and tool-neutral handlers call one authenticator that checks expiry, revocation, and disable before it returns the principal.
2. **Last-used write does not fail authentication.** Last-used update is a separate write that never blocks authentication. A failure to record use leaves the token usable and the next request retries the write.

## Structure

Unexported field `live bool` on the token record with constructor `newAccessToken` that checks expiry against the maximum lifetime; only the constructor makes a live token.

## Risks

- Last-used writes on every request contend on the token row under load; the write runs outside the authentication transaction with a bounded retry.
- Clock skew between instances is bounded by strict expiry comparison with no grace window; a token at or past expiry never authenticates.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Authenticator rejects expired, revoked, and disabled tokens and checks maximum lifetime at create and rotation time. | Send an expired token, a revoked token, and an idle-disabled token directly to the authenticator and check each gives denial. |
| port adapter | Store query returns only live tokens for authentication and applies the fence in the same transaction for state writes. | Insert an expired row and a disabled row, then query the live-token view and check neither row returns. |
| HTTP handler | HTTP handlers map denial to 401 with a stable error code and no secret in the body, and record the refusal in the audit log. | Send a request with an expired bearer token to the Pulumi and TFE routes and check each answers 401. |
