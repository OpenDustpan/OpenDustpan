# 55. Sign-in and provisioning methods in milestone 4

Status: accepted · 2026-10-13. Decision 9 changed by ADR 297: a removal disables the person, and the server keeps no session for review

Changed by:

- ADR 70 changes decisions 4, 9 and 11: the SCIM groups, removals and the
  sign-in settings action.
- ADR 285 changes decision 5: a local user signs in with a passkey, a password
  or both.
- ADR 297 changes decision 9: the server disables the person and keeps no session.

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

On 2026-10-13 the owner also answered three questions on this list:

- What "OAuth" covers: "Any OAuth 2.0 provider".
- What a SCIM or LDAP removal does: "Disabled by default, with opt-in
  auto-revoke." The project lead read this answer as follows. A removal
  disables the sessions and the access tokens of the user, and the server
  keeps them for review. A setting turns on revocation at once instead.
- Where IP allowlists apply: "IP allowlists need to be enabled by the admin
  first, with opt-in per namespace/access token/etc". This corrects an
  earlier summary that the project lead gave.

## Decision

1. **All of these are in the binary in milestone 4.** An operator does not
   need a proxy for them.

   | Method | Kind |
   | --- | --- |
   | LDAP | `Authenticator` adapter: a directory check of a user name and a password |
   | SAML | `Authenticator` adapter: SSO sign-in in the browser |
   | CAS | `Authenticator` adapter: SSO sign-in in the browser |
   | OAuth | `Authenticator` adapter: sign-in in the browser through any OAuth 2.0 provider |
   | SCIM 2.0 | Provisioning adapter: users and groups from an identity provider |
   | Passkeys for local users | Part of the local-user `Authenticator`: a sign-in with a WebAuthn passkey and no password |
   | Trusted-header sign-in | `Authenticator` adapter: the identity comes from a header that a trusted proxy sets |
   | IP allowlists | A check on the source address of a request, for console sign-in and for access tokens |

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
7. **Any OAuth 2.0 provider, with a configured identity.** ADR 6 says that
   OAuth 2.0 alone is authorization, not sign-in. So for a provider with no
   OIDC, an administrator configures a user-info endpoint and a claim
   mapping. The server calls that endpoint with the access token of the
   provider, and the mapping names the claim that identifies the person.
   For an OIDC provider, the ID token identifies the person. ADR 6 stays
   true: the provider only proves who the person is.
8. **IP allowlists are off by default, and each scope opts in.** An
   administrator of the installation turns the feature on first. Then each
   scope opts in on its own: the installation, each namespace and each
   access token. A scope that did not opt in has no list. When the feature
   is on, each list applies to console sign-in and to requests with an
   access token.
9. **A removal disables, and revocation is opt-in.** When SCIM or LDAP
   removes a user, the server disables the sessions and the access tokens
   of the user. It keeps them for review. A setting
   makes the server revoke them at once instead. The server writes an audit
   entry for each disable and each revocation.
10. **MFA follows ADR 46.** With an SSO identity provider (SAML, CAS,
    OAuth), MFA is the job of that provider. For local users and LDAP,
    built-in MFA applies.
11. **API.** The configuration of each method that changes at run time has
    a REST API and an action (ADR 17). Each change writes an audit entry.

## Not decided here

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) keeps these open:

- How lists at more than one scope combine. For example, must a request
  pass both the list of its namespace and the list of its token?
- How the server finds that LDAP removed a user: at the next sign-in, with
  a sync, or both.
- Whether trusted-header sign-in gives groups as well as a user.
- Whether "require MFA" (ADR 46) applies to the bootstrap admin token.

## Consequences

- Each method has an e2e step with a real or a test server (an LDAP server,
  a SAML and a CAS identity provider, an OAuth provider, a SCIM client)
  before it ships.
- LDAP, SAML, CAS, OAuth, SCIM and WebAuthn libraries can be new
  dependencies. Ask the project owner before adding each one.
