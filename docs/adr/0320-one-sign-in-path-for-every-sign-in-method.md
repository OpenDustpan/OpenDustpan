# 320. One sign-in path for every sign-in method

Status: accepted · 2026-11-02. The design is the proposal P1 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md), with decisions of the PM under the owner's delegation. The owner accepts them with the merge. The owner decisions that it uses are in ADR 55, ADR 70, ADR 285, ADR 297 and ADR 301

Changes:

- Changes ADR 55 decisions 1 and 2: the port of a sign-in method is one of three method ports in `pkg/session`, not `identity.Authenticator`.
- Extends ADR 70 decision 12: a membership can also come from LDAP, with the source `ldap`.
- Extends ADR 227 decision 1: the request and the check of a fresh authentication for each method.
- Extends ADR 285 decision 21: the rule of the fresh step of a password applies to LDAP too.

## Context

ADR 6 says that a sign-in method only proves who a person is. OpenDustpan
then issues its own session or token. ADR 55 puts LDAP, SAML, CAS, OAuth,
passkeys for local users and the trusted header in the binary. Its decision
2 says that each method is an adapter behind a port with a conformance
suite. ADR 70 decisions 12 to 22 give the rules for groups from an identity
provider. ADR 216 decision 9 says that a sign-in finds or makes a principal
of its own home only. ADR 227 gives one fresh-authentication window. ADR 297
puts the state of a sign-in in sealed cookies. ADR 301 gives the fresh step
to a person of plain OAuth 2.0 or of a trusted header.

Facts on `main` at bc4587e:

- `identity.Authenticator` has one method, `AuthenticateToken`. It resolves
  an access token. It cannot carry a redirect, a password or the facts of a
  proxy request.
- Browser sign-in exists for OIDC and plain OAuth 2.0
  (`internal/oauth`). `oauth.Identity` has no groups.
- `principal.SignInPrincipal` finds or makes the principal of a subject.
  `principal.MembershipSource` has `local`, `scim` and `claim`. No code
  applies a group claim.
- `internal/authgate` has the source budget (`FailureCounter`) and the
  lockout by name (`NameLockCounter`) of ADR 285 decisions 14 and 15.
- `session.FreshKind` has `passkey`, `mfa`, `password` and `sso`.

RFC 21 P1 proposes one path for each method, and the counts of sign-in
failures. This ADR records it.

## Decision

### The ports

1. **Three shapes of method, one result.** Each sign-in method implements
   one of three ports in `pkg/session`. `identity.Authenticator` stays the
   port of access tokens only.

   | Shape | Port | Methods |
   | --- | --- | --- |
   | Redirect | `RedirectMethod`: `Begin(ctx, BeginInput) (Redirect, error)` and `Finish(ctx, Callback) (Assertion, error)` | OIDC, plain OAuth 2.0, SAML, CAS |
   | Password | `PasswordMethod`: `Verify(ctx, Credentials) (Assertion, error)` | LDAP |
   | Request | `RequestMethod`: `FromRequest(ctx, RequestFacts) (Assertion, error)` | trusted header |

   The local password (ADR 285) and the passkey sign-in are not adapters of
   these ports. They are part of the local-user sign-in, as ADR 55 decision 5
   and ADR 285 say. They make the same `Assertion`, with the method kinds
   `local-password` and `passkey`.
2. **The Assertion.** A method returns a `session.Assertion`. It holds:
   - the provider ID and the subject at the identity provider,
   - the user name, the display name candidates, the email and whether the
     provider verified the email,
   - the groups, flat, and whether the method sent groups at all
     (`GroupsPresent`),
   - the authentication time and whether the method proves it
     (`AuthTimeKnown`),
   - the kind of the method,
   - the factors that OpenDustpan checked itself (`password`, `passkey`,
     `totp`, `recovery-code`, `ssh-signature`). A redirect method and a
     request method return none, because the provider does not tell which
     factors it checked.

   A method never makes a principal, a membership or a session. A method can
   keep its own one-use records, for example the used SAML assertion IDs and
   CAS tickets.
3. **The limits of an Assertion.** A subject has 1 to 255 bytes and no
   control character. An Assertion holds at most 1000 groups of 1 to 256
   bytes each, with no control character. A name, a user name and an email
   have at most 256 bytes. A method that receives more groups refuses the
   sign-in with `ErrMethodAnswer`. It never cuts the list to the limit. The
   sign-in service checks each Assertion again before it uses it. Defense in
   depth: the adapter checks the answer of its protocol, and the service
   check stays so that a new adapter cannot skip it.
4. **No `net/http` in the ports.** `Callback` and `RequestFacts` are plain
   structs. The HTTP adapter copies each field from the request:
   - `Callback`: the pending request, the state, the code, the ticket, the
     error of the provider and the decoded SAML response.
   - `RequestFacts`: the peer address, the client address from
     `internal/clientip`, only the headers that the method names, and the
     TLS client chain after the check against the trust bundle.
   The pending request of a redirect method (state, nonce, PKCE verifier,
   SAML request ID, start time, and whether it asked for a fresh
   authentication) is in a sealed cookie, as `RequestClaims` of ADR 297 is
   today.
5. **One set of errors.** Each adapter translates the errors of its
   protocol, its library and its network into the errors of `pkg/session`:
   `ErrMethodInput`, `ErrMethodRejected`, `ErrSubjectDisabled`,
   `ErrMethodUnavailable`, `ErrMethodAnswer`, `ErrRequestBinding`,
   `ErrReplay`, `ErrUntrustedSource`, `ErrNoAssertion`, `ErrGroupsMissing`
   and `ErrProviderDisabled`. The doc comment of each port names the error of
   each case. A method never returns an error of a driver or a library.
6. **Suites.** `sessiontest.RunRedirectMethod`, `RunPasswordMethod` and
   `RunRequestMethod` hold the cases of each port, and
   `principaltest.RunGroupClaims` holds the cases of decision 10. Each case
   that sends hostile input tests one layer of CLAUDE.md alone. Each case
   skips until an adapter exists. Each adapter runs its suite.

### The path

7. **One order for each method.** After a method returns an Assertion, the
   sign-in service does these steps, in this order:
   1. It checks the Assertion (decision 3).
   2. It checks that the provider is on and belongs to the home of the
      principal (ADR 216 decision 9).
   3. It calls `principal.SignInPrincipal` with the subject
      `<provider ID>:<subject>`.
   4. For `ErrSubjectDisabled`, it disables the principal of the subject
      (ADR 70 decision 19, with the bump of the session generation of ADR
      297 decision 12), writes the audit entry and refuses the sign-in.
   5. It calls `principal.ApplyGroupClaims` when `GroupsPresent` is true.
   6. It asks for a built-in factor when ADR 46, ADR 285 or ADR 301 asks for
      one. The sign-in is then a pending sign-in (ADR 297 decision 20).
   7. It issues the cookies of the session (ADR 297) with the method kind,
      the factors and the authentication time.

   An error at a step refuses the sign-in and changes nothing of the later
   steps. A refused group add (decision 11) is not an error. The sign-in
   goes on without that membership.
8. **One answer, and the reason in the audit log.** The browser receives one
   answer for each refused sign-in of a method. A password method uses the
   answer of ADR 285 decision 13: `401` and "The user name or the password
   is wrong.". The audit entry of the refusal records the real reason, the
   provider ID and the method kind. It never records a password, a ticket, a
   code or an assertion.
9. **Subjects.** The subject of a principal stays
   `<provider ID>:<subject at the identity provider>`. Each mode names the
   attribute that gives the subject. The default is the attribute that the
   protocol defines as stable (ADR 321). A change of the attribute is a
   sensitive change with an alert (ADR 70 decision 22).

### Groups from a method

10. **One function for group claims.** `principal.ApplyGroupClaims` applies
    the groups of one identity provider to the memberships of one principal.
    A sign-in calls it with the groups of the Assertion. A SCIM change of the
    groups of a user calls it too. It holds ADR 70 decisions 13 to 22. The
    port is `principal.GroupClaimApplier`. Its rules:
    - It changes only the memberships of its source and source ID. It never
      changes a local membership, a membership of another provider or a
      pinned membership (ADR 70 decision 20).
    - `GroupsPresent` false changes nothing. An empty list that is present
      ends each membership of the source.
    - A name matches the group of that name in the home of the principal. A
      group name is unique in its home (RFC 19). A claim joins a `local`
      group, or a directory group of its own source. It never makes a group.
      A name that matches no group goes in the list `Unknown` of the result
      and in the audit entry.
    - It refuses a principal that the identity provider cannot manage: a
      principal that is not a person, a break-glass account and a temporary
      administrator. Defense in depth: `SignInPrincipal` refuses these too,
      and this check stays so that a SCIM caller cannot skip it.
11. **The rules of ADR 70 apply as they are.** A protected group refuses the
    add unless it trusts the identity provider or full sync is on (ADR 70
    decisions 14, 16 and 17). A group that a person closed refuses the add
    (decision 15). A removal from a protected group suspends the membership,
    and full sync makes it final (decision 20). The removals count for the
    mass-suspension alert, with the limit of ADR 306 decision 2. Each add,
    removal, suspension and refused add writes an audit entry in the
    transaction of the change.
12. **The source `ldap`.** A membership from an LDAP directory, at a sign-in
    or at a sync, has the source `ldap` and the provider ID as its source ID.
    A group claim of OIDC, OAuth 2.0, SAML, CAS or a trusted header has the
    source `claim` and the provider ID. SCIM keeps the source `scim`, with the
    SCIM connection as its source ID. The first LDAP adapter adds `ldap` to
    `principal.ParseMembershipSource` and to the store suites.

### Fresh authentication

13. **Each method asks for a fresh authentication in its own way.** The
    fresh step of ADR 227 decision 1 uses the parameter of each protocol:

    | Method | How the server asks | What the server checks | Fresh kind |
    | --- | --- | --- | --- |
    | OIDC | `max_age=0` | `auth_time` in the ID token | `sso` |
    | SAML | `ForceAuthn="true"` | `AuthnInstant` of the `AuthnStatement` | `sso` |
    | CAS | `renew=true` on `/login` and on `/p3/serviceValidate` | the validation of a ticket from a new primary sign-in | `sso` |
    | LDAP | a new bind with the password | the time of the bind, with the rule of decision 14 | `password` or `mfa` |
    | Plain OAuth 2.0 | none | none | ADR 301 |
    | Trusted header | none | none | ADR 301 |

    A fresh step of OIDC, SAML or CAS counts only when `AuthTimeKnown` is
    true and the authentication time is not before the start of the request
    that asked for it. The skew of the method applies. An authentication
    time in the future, after the skew, refuses the step.
14. **LDAP is a password method.** For the fresh step, an LDAP person
    follows ADR 285 decision 21, as a local password user does. A password
    alone is a fresh authentication only for a person with no factor, in an
    installation with no SSO provider where MFA is not required. Else the
    fresh step is a password and a second factor (`mfa`), or a passkey.

### Sign-in failures

15. **LDAP uses the lockout by name of ADR 285.** The LDAP sign-in uses the
    counts of ADR 285 decision 14 (`password`, `passwordname` and `factor`)
    and the source budget of decision 15, with the same numbers. The name of
    a count is the keyed digest of the provider ID and the folded user name.
    So the counts of one user name at two providers stay apart, and an
    unknown name locks the same as a known one. The service adds each
    failure before it calls `Verify`, and clears the counts only after the
    commit of a full sign-in. A directory that does not answer leaves the
    count. This is the safe direction.
16. **A passkey sign-in counts by client address.** A discoverable passkey
    sends no user name, and the client chooses the credential ID. A count by
    credential ID would let anyone who knows the ID lock the person out. So
    the passkey sign-in counts the failed assertions of each client address
    (an IPv4 address, or the /64 of an IPv6 address) in the source budget,
    in its own scope `passkey`. 20 failures in 15 minutes refuse that
    address for 15 minutes. A passkey cannot be guessed, so this limit stops
    load, not guesses. The passkey sign-in reads no name lock and no factor
    lock (ADR 285 decision 14).
17. **A break-glass account has no lockout by name.** It signs in with a
    passkey or a hardware key only (ADR 70 decision 23), so the counts of
    decision 15 do not apply to it. The limit by address of decision 16
    applies. At each 10 failed assertions for a credential of a break-glass
    account, the server sends an alert to each site administrator. So no
    "blocked" state exists that the guard of ADR 70 decision 24 must count.
18. **A redirect method and a request method have no lockout by name.** The
    identity provider or the proxy checks the password. The routes of the
    callback keep the rate limits of the server.

### Providers

19. **Each mode has its own settings.** `session.ProviderMode` adds `saml`,
    `cas`, `ldap` and `header`, each with its own settings struct in
    `ProviderConfig`, as `OAuthSettings` is today. The provider admin routes
    of today serve each mode. Each adapter adds its mode in its own change.

## Not decided here

- The design of each method: ADR 321.
- SCIM, built-in MFA enrollment, WebAuthn, TOTP, recovery codes, IP
  allowlists, mTLS, the token expiry email and break-glass mode 2. RFC 21
  P7 to P12 and their later ADRs hold them.

## Consequences

- RFC 21 work item W2 becomes the implementation of these ports, the
  `ApplyGroupClaims` service and the order of decision 7. Each method of ADR
  321 then needs only its adapter, its settings and its e2e step.
- The OIDC and plain OAuth 2.0 sign-in of today move behind
  `RedirectMethod` in W2. Their behavior does not change, except the groups
  claim of ADR 321.
- `session.Assertion` and `principal.GroupClaims` are new types. No data
  changes, because no tag shipped them.
- `principal.ParseMembershipSource` accepts `ldap` with the first LDAP adapter.
  The conformance suite of the principal store then accepts it.
