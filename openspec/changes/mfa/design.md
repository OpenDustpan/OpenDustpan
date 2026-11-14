## Context

Built-in MFA for local and LDAP sign-in with require MFA on, no added step for SSO, and fresh-step checks for token creation guarded at POST /api/dustpan/v1/me/tokens, token rotation, and secret reveal.

## Decisions

1. **Service-owned MFA gate.** Enforce MFA in the sign-in service, not in each HTTP adapter. The service creates a pending sign-in for a password first factor and issues a session only after a valid second factor with user verification preferred for passkeys.
2. **Cookie-bound freshness time.** Record the fresh step in the sealed access cookie of the sign-in that did it, and in no other sign-in. Token creation, token rotation, and secret reveal read that sealed value, refuse a cookie from another sign-in, and compare its age with the one freshAuthSeconds window owned by fresh-authentication-window-0-turns.
3. **One factor store.** Keep one factor store in pkg/mfa with service internal/mfasvc. Enrollment, recovery codes shown once, and the one administrator reset route live here; app-level-second-factor-totp and first-step-up-key-in reuse this store and add no second store.

## Structure

Unexported mfa.Verifier built only by NewVerifier, which checks the enrolled factors and the window; no session issues without VerifySecondFactor.

## Risks

- TOTP clock drift locks a person out.
- Lost factors need an administrator reset path.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | HTTP adapter rejects malformed factor input and enforces rate limits before the service runs. | Send wrong TOTP codes and replayed WebAuthn assertions directly to the handler; each fails and the failure counter grows. |
| Service | Sign-in service checks enrollment, verifies the second factor with single use of TOTP codes, and never issues a session from the first factor alone. | Call the service with a valid password but a wrong or reused TOTP code; no session is issued. |
| Port adapter | MFA store keeps secrets sealed and writes an audit entry for each enrollment, use, and reset. | Replay a used TOTP code and read the stored secret row directly; the replay fails and the secret stays sealed. |
