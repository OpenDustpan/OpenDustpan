## Context

Persons who sign in with plain OAuth 2.0 or trusted-header sign-in have no provider proof of fresh authentication. This change adds app-level TOTP, passkey and SSH-signature factors that count as a fresh step inside the one freshness window.

## Decisions

1. **One factor service.** Reuse the local-user MFA factor service for enrollment, challenge, check and audit entries behind one Factor port in internal/mfasvc, with no separate OAuth-only copy and no new WebAuthn dependency. The mfa change owns the factor store, admin reset and recovery codes.
2. **Fresh step bound to one sign-in.** Record the fresh second-factor step in the sealed access cookie of the sign-in that did it, with kind passkey, totp or ssh and time, and check it with the one freshness window; a fresh step in one sign-in never passes another sign-in. The fresh-authentication-window-0-turns change owns the zero-window behavior.
3. **First-enrollment proof and sealed storage.** Enroll the first factor of a person with no factor only with an administrator enrollment code with 128-bit randomness, single use and a one-hour life or a one-time first-enrollment-only email code; seal TOTP secrets and WebAuthn credentials with the installation key hierarchy and show a secret one time.

## Structure

Unexported type factorService in internal/mfasvc with constructor newFactorService that checks the store port, the clock and the allowlisted WebAuthn config; only the constructor makes valid challenge state, which removes replay and cross-origin bypass as a bug class.

## Risks

- A stolen session enrolls the first factor without an independent proof.
- A weak email channel becomes a sign-in factor instead of a one-time enrollment proof.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter boundary check | Allowlist factor type, base32 TOTP secret shape, WebAuthn origin and RP ID check at the API boundary; reject invalid input without normalization. | Send unknown factor type, malformed secret and cross-origin WebAuthn response directly at the handler and expect 400 with no state change. |
| service check | Constant-time TOTP compare, single-use challenge nonces in durable storage that survives restart, sealed storage of factor secrets. | Replay a used TOTP code and a used WebAuthn assertion, including across restart and with a stolen cookie from another sign-in, and expect rejection. |
| HTTP handler authorization | Authorization action on each factor route for the person object, fresh-step check at the point of use for token creation, secret reveal and factor changes. | Call token creation with a session whose fresh step belongs to another sign-in and expect 403 mfa.fresh-step-required. |
