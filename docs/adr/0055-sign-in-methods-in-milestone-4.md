# 55. Sign-in and provisioning methods in milestone 4

Status: accepted · 2026-10-13

## Context

ADR 6 makes `Authenticator` a port. It says that operators want any sign-in
standard (OIDC, SAML, CAS, LDAP, local users), and that a sign-in adapter only
signs a person in so that the server can issue a token or a session. Today
the binary has one adapter: the static bootstrap admin token. ADR 46 adds
built-in MFA for installs with no SSO identity provider.

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) asks which sign-in
methods and which hardening belong in the binary, and which belong to the
proxy of the operator. On 2026-10-13 the project owner answered with this
list for milestone 4: "LDAP, SAML, CAS, OAuth, SCIM 2.0, Passkeys for local
users, Trusted-header sign-in, IP allowlists".

## Decision

1. **All of these are in the binary in milestone 4.** An operator does not
   need a proxy for them.

   | Method | Kind |
   | --- | --- |
   | LDAP | `Authenticator` adapter: a directory check of a user name and a password |
   | SAML | `Authenticator` adapter: SSO sign-in in the browser |
   | CAS | `Authenticator` adapter: SSO sign-in in the browser |
   | OAuth | `Authenticator` adapter: sign-in in the browser through an OAuth provider |
   | SCIM 2.0 | Provisioning adapter: users and groups from an identity provider |
   | Passkeys for local users | Part of the local-user `Authenticator`: a sign-in with a WebAuthn passkey and no password |
   | Trusted-header sign-in | `Authenticator` adapter: the identity comes from a header that a trusted proxy sets |
   | IP allowlists | A check on the source address of a request |

2. **Each method is an adapter behind a port.** Sign-in methods implement
   the `Authenticator` port of ADR 6. SCIM implements a provisioning port.
   Each port has a conformance suite, and each adapter runs it (CLAUDE.md).
3. **OpenDustpan still issues the tokens.** A sign-in method only proves who
   a person is. The server then issues its own session or access token
   (ADR 6). Tool CLIs keep using access tokens.
4. **SCIM feeds groups.** SCIM 2.0 creates, changes and removes users and
   groups. Its groups and memberships are the groups of ADR 52.
5. **Passkeys for local users are a sign-in, not only a second factor.** A
   local user can sign in with a passkey alone. WebAuthn as a second factor
   stays as ADR 46 says.
6. **Trusted-header sign-in is off by default.** The operator turns it on
   and names the proxies that may set the header. The server ignores the
   header from any other source.
7. **MFA follows ADR 46.** With an SSO identity provider (SAML, CAS, OAuth),
   MFA is the job of that provider. For local users and LDAP, built-in MFA
   applies.
8. **API.** The configuration of each method that changes at run time has a
   REST API and an action (ADR 17). Each change writes an audit entry.

## Not decided here

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) keeps these open:

- What "OAuth" covers. ADR 6 says that OAuth 2.0 alone is authorization and
  OIDC is the login standard. Does the adapter support OIDC only, or also
  OAuth 2.0 providers with no OIDC?
- Whether an IP allowlist applies to the whole installation, to a namespace,
  or to both, and whether it applies to access tokens as well as to console
  sign-in.
- What a SCIM removal does to the access tokens and the sessions of the
  removed user.
- Whether trusted-header sign-in gives groups as well as a user.
- Whether "require MFA" (ADR 46) applies to the bootstrap admin token.

## Consequences

- Each method has an e2e step with a real or a test server (an LDAP server,
  a SAML and a CAS identity provider, an OAuth provider, a SCIM client)
  before it ships.
- LDAP, SAML, CAS, OAuth, SCIM and WebAuthn libraries can be new
  dependencies. Ask the project owner before adding each one.
