# 46. Built-in MFA for installs with no SSO identity provider

Status: accepted · 2026-10-12. Decision 8 extended by ADR 212 decision 3

Changed by:

- ADR 55 extends decision 3: passkey sign-in and the open sign-in hardening
  items.
- ADR 56 extends decision 6: the MFA freshness window.
- ADR 57 extends: recovery after the loss of every factor and code.
- ADR 212 extends decision 8: an access token gets a refusal for a fresh MFA
  step.
- ADR 285 changes decisions 1 and 2: local password users need MFA also next
  to an SSO provider.

## Context

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) found that an install
that signs people in with a local user name and password, or with LDAP, has no
second factor. ADR 6 says that SSO adapters sign humans in, and that
OpenDustpan issues its own access tokens. On 2026-10-11 the project owner
chose built-in MFA for these installs. On 2026-10-12 the owner edited the
choice: WebAuthn and TOTP are options of the same rank, and an administrator
sets the MFA for a secret reveal apart from the other MFA settings.

## Decision

1. **Built-in MFA.** An install that signs people in with no SSO identity
   provider (local users, LDAP) can use built-in MFA.
2. **With an SSO identity provider, MFA is the job of that provider.**
   OpenDustpan adds no MFA step of its own on top (ADR 6).
3. **Two kinds of second factor, of the same rank.** WebAuthn (passkeys,
   Touch ID, Windows Hello, hardware keys) and TOTP. A person can use either.
4. **Recovery codes.** The server shows recovery codes one time, at
   enrollment.
5. **Off by default, for any number of users.** An administrator turns MFA on.
   Then the administrator can require it for all users.
6. **What MFA covers.** Console sign-in, the creation of access tokens, and
   the reveal of a secret value (ADR 41).
7. **A separate setting for the secret reveal.** An administrator can require
   MFA for a secret reveal with a setting that is separate from the setting
   that requires MFA for all users.
8. **Access tokens stay bearer tokens.** A CLI or CI access token needs no MFA
   step on each use. A person creates a CLI access token after an MFA
   sign-in, and the token has an expiry (RFC 20).
9. **Reset.** An administrator can reset the MFA of a user. Each reset writes
   an audit entry (ADR 41).
10. **API.** Each MFA operation has a REST API and an action (ADR 17).

## Not decided here

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) keeps these open:

- Which sign-in hardening belongs in the binary, and which belongs to the
  proxy of the operator.
- How a user signs in after they lose every factor and every recovery code.
- Whether "require MFA" applies to the bootstrap admin token.
- How long an MFA sign-in stays valid for the creation of access tokens and
  for a secret reveal.
- SCIM provisioning, trusted-header sign-in and IP allowlists. They stay as
  first ideas in the RFC.

## Consequences

- The server stores factors and recovery codes per principal. This needs
  numbered migrations in each SQL dialect (CLAUDE.md).
- A WebAuthn or TOTP library is a new dependency. Ask the project owner
  before adding it.
- Each MFA route needs an action and an object in the route test (ADR 17).
- A reveal of a secret value (ADR 41) must check the separate setting of
  decision 7.
