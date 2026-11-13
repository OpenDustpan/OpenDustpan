## Context

Token checks run on every authenticated request for Pulumi CLI and OpenTofu paths. Token routes live under /api/dustpan/v1/me/tokens. Inactivity and SMTP keys live in the generic settings API. Warning delivery runs in background and does not block requests or leak secrets.

## Decisions

1. **Check order at the authenticator.** Store lastUsedAt, expiresAt and disabled state on the token record; check expiry then revocation then disabled at the authenticator port before authorization.
2. **Warning state derived, email queued once.** Compute warning state from expiresAt and the two warning start settings at read time; queue email once per threshold crossing with an idempotent job key and queue backlog warnings at the next sweep.
3. **Single clock source with tolerance.** Evaluate expiry and inactivity against database transaction time with a 30 second tolerance for process clock differences.
4. **Atomic last-used update.** Update lastUsedAt with an atomic compare-and-set that never clears a concurrent disable transition.

## Structure

An unexported tokenLifetime checker with constructor that loads maximum lifetime, inactivity period and warning starts; only the constructor builds valid cutoff times.

## Risks

- Clock differences across server processes mark a live token expired or keep an expired token live.
- Warning email without SMTP settings leaves the owner without notice unless the API state stays visible.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Authenticator port rejects expired, revoked and disabled tokens before any domain action; unit tests send expired and disabled tokens directly to the port. | Send a request with an expired token with a valid signature and confirm 401 with no state read or write. |
| service | Service compares database time expiry and inactivity cutoff from stored times and refuses with domain sentinels; tests move the clock past expiry and past inactivity. | Backdate lastUsedAt past the inactivity period and confirm the next use disables the token and writes an audit entry. |
| HTTP handler | HTTP adapter maps sentinels to 401 for expired or revoked and 403 for disabled with no token value in logs; handler tests assert status and error code. | Replay a disabled token against Pulumi and TFE routes and confirm the contract status and no secret in the response. |
