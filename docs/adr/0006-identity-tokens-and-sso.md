# 6. Identity: OpenDustpan issues tokens; SSO only signs humans in

Status: accepted · 2026-10-08

Changed by:

- ADR 17 changes: the authorizer takes an object reference, not only a
  namespace.
- ADR 52 extends: groups, roles and grants.
- ADR 53 extends: token kinds, lifetimes, inactivity and retiring the
  bootstrap token.
- ADR 237 changes: the bootstrap admin token of dev comes from other sources.
- ADR 339 extends: the bootstrap admin token asks for the one-time code of the first administrator.

## Context

Operators want any SSO standard (OIDC, SAML, CAS, LDAP, local users). The
Pulumi CLI always authenticates with an access token.

## Decision

- Authentication and authorization are separate ports: `Authenticator`
  (token to principal) and `Authorizer` (principal, action, namespace).
- OpenDustpan always issues its own access tokens. SSO adapters only
  authenticate humans in the browser so a token or session can be minted.
  (OAuth 2.0 alone is authorization; OIDC is the login standard. LDAP is a
  directory credential check, not single sign-on.)
- The bootstrap adapter is a single static admin token from
  `DUSTPAN_ADMIN_TOKEN` (environment only, never a flag, at least 32 bytes,
  compared in constant time). The default authorizer allows administrators only.

## Consequences

- The static token is for first boot. Real users, tokens and roles are the next
  identity work.
