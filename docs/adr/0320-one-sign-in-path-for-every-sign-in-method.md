# 320. One sign-in path for every sign-in method

Status: accepted · 2026-11-02. The design is the proposal P1 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md), with decisions of the PM under the owner's delegation, marked "(PM)". The owner accepts them with the merge. A decision marked "(owner, ADR N)" repeats an owner decision of that ADR. The rule that maps a group value of an identity provider to a group waits for an owner decision
Changed by:

- ADR 332 changes decision 12: the group mapping is a rule for each provider, with explicit single mappings.


Changes:

- Changes ADR 55 decisions 1 and 2: the port of a sign-in method is one of three method ports in `pkg/session`, not `identity.Authenticator`.
- Extends ADR 70 decision 12: LDAP memberships have the source `ldap`. SAML, CAS and trusted-header persons have their own principal source.
- Extends ADR 227 decision 1: the request and the check of a fresh authentication for each method, bound to the person of the session.
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

Facts on `main` at 6c35afa:

- `identity.Authenticator` has one method, `AuthenticateToken`. It resolves
  an access token. It cannot carry a redirect, a password or the facts of a
  proxy request.
- Browser sign-in exists for OIDC and plain OAuth 2.0
  (`internal/oauth`). `oauth.Identity` has no groups.
- `principal.SignInPrincipal` finds or makes the principal of a subject. It
  refuses a local subject (`local:`) and a break-glass account
  (`ErrNotSignInTarget`). `principal.Source` has `local`, `ldap`, `scim`,
  `oidc` and `oauth`. `principal.MembershipSource` has `local`, `scim` and
  `claim`. No code applies a group claim.
- `internal/localusersvc` signs in a local user with a password, keeps the
  lockout by name of ADR 285 decisions 14 to 16, and serves
  `GET /api/dustpan/v1/local-users/locks` and
  `POST /api/dustpan/v1/local-users/unlock`.
- `internal/authgate` has the source budget (`FailureCounter`) and the
  counts of the lockout by name (`NameLockCounter`).
- `session.FreshKind` has `passkey`, `mfa`, `password` and `sso`.
- The table `principal_groups` has no home column, and its `name_key` is
  unique across the installation. No column stores the trust of a group,
  its close switch or the full-sync setting.

ADR 70 decision 22 calls "a claim mapping" a sensitive setting. ADR 70 does
not say how a group value of an identity provider finds a group.

## Decision

### The ports

1. **Three shapes of method, one result (PM).** Each sign-in method of an
   identity provider implements one of three ports in `pkg/session`.
   `identity.Authenticator` stays the port of access tokens only.

   | Shape | Port | Methods |
   | --- | --- | --- |
   | Redirect | `RedirectMethod`: `Begin(ctx, BeginInput) (Redirect, error)` and `Finish(ctx, Callback) (Assertion, error)` | OIDC, plain OAuth 2.0, SAML, CAS |
   | Password | `PasswordMethod`: `Verify(ctx, Credentials, BindGate) (Assertion, error)` | LDAP |
   | Request | `RequestMethod`: `FromRequest(ctx, RequestFacts) (Assertion, error)` | trusted header |

   The local password (ADR 285) and the passkey sign-in are not adapters of
   these ports. They read their own records, and they make the same
   `Assertion` with the method kinds `local-password` and `passkey`
   (decision 8).
2. **The Assertion (PM).** A method returns a `session.Assertion`. It holds:
   - the provider ID and the subject at the identity provider,
   - the principal ID, for the local password and the passkey only,
   - the user name, the display name candidates, the email and whether the
     provider verified the email,
   - the group values, flat, and whether the method sent groups at all
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
3. **The limits of an Assertion (PM).** A subject has 1 to 255 bytes and no
   control character. An Assertion holds at most 1000 group values of 1 to
   256 bytes each, with no control character. A name, a user name and an
   email have at most 256 bytes. A method that receives more groups refuses
   the sign-in with `ErrMethodAnswer`. It never cuts the list to the limit.
   The sign-in service checks each Assertion again before it uses it, and
   refuses an Assertion of a method port that sets a principal ID. Defense
   in depth: the adapter checks the answer of its protocol, and the service
   check stays so that a new adapter cannot skip it.
4. **No `net/http` in the ports (PM).** `Callback` and `RequestFacts` are
   plain structs. The HTTP adapter copies each field from the request:
   - `Callback`: the pending request (a pointer, nil when the browser holds
     none), the state, the code, the ticket, the error of the provider and
     the decoded SAML response.
   - `RequestFacts`: the peer address, the client address from
     `internal/clientip`, only the headers that the method names, and the
     TLS client chain after the check against the trust bundle.

   The pending request of a redirect method is in a sealed cookie, as
   `RequestClaims` of ADR 297 is today. It holds the state, the nonce, the
   PKCE verifier, the SAML request ID, the start time, and whether it asked
   for a fresh authentication. For a fresh request, it also holds the
   principal ID and the sign-in ID of the session that asked.
5. **One set of errors (PM).** Each adapter translates the errors of its
   protocol, its library and its network into the errors of `pkg/session`:
   `ErrMethodInput`, `ErrMethodRejected`, `ErrSubjectDisabled`,
   `ErrMethodUnavailable`, `ErrMethodAnswer`, `ErrRequestBinding`,
   `ErrReplay`, `ErrUntrustedSource`, `ErrNoAssertion`, `ErrGroupsMissing`
   and `ErrProviderDisabled`. The sign-in service adds `ErrInvalidAssertion`
   (decision 3) and `ErrFreshSubject` (decision 18). The doc comment of each
   port names the error of each case. A method never returns an error of a
   driver or a library.
6. **Suites (PM).** `sessiontest.RunRedirectMethod`, `RunPasswordMethod` and
   `RunRequestMethod` hold the cases of each port. `principaltest.RunGroupMapper`
   and `RunGroupClaims` hold the cases of decisions 12 and 13. Each case that
   sends hostile input tests one layer of CLAUDE.md alone. Each case skips
   until an adapter exists. Each adapter runs its suite.
   `session.CheckFreshSubject` (decision 18) is code now, with its own
   tests.

### The path

7. **One order for each method (PM).** After a method returns an Assertion,
   the sign-in service does these steps, in this order:
   1. It checks the Assertion (decision 3).
   2. It checks that the provider is on and belongs to the home of the
      principal (ADR 216 decision 9).
   3. It finds the principal. A method port uses
      `principal.SignInPrincipal` with the subject
      `<provider ID>:<subject>` and the source of decision 10. The local
      password and the passkey use the branch of decision 8.
   4. For `ErrSubjectDisabled`, it does the steps of decision 9 and refuses
      the sign-in.
   5. It calls `principal.ApplyGroupClaims` when `GroupsPresent` is true.
   6. It asks for a built-in factor when ADR 46, ADR 285 or ADR 301 asks for
      one. The sign-in is then a pending sign-in (ADR 297 decision 20).
   7. It issues the cookies of the session (ADR 297) with the method kind,
      the factors and the authentication time.

   An error at a step refuses the sign-in and changes nothing of the later
   steps. A refused group add (decision 13) is not an error. The sign-in
   goes on without that membership.
8. **The local branch (PM).** The local password and the passkey find the
   principal in their own records, with the code that exists:
   - The local password: `localusersvc.Service.SignIn` reads the local user
     by its user name, checks the password and the state of the principal,
     and keeps the lockout of ADR 285 decisions 13 to 16. It returns the
     principal ID.
   - The passkey: the WebAuthn store of `pkg/mfa` reads the credential of
     the assertion and its person, and checks the state of the principal.
     This is the path of a break-glass account (ADR 70 decision 23).

   Both set `ProviderID` to `local` and set `PrincipalID`. They skip step 3
   of decision 7, because `SignInPrincipal` refuses a local subject and a
   break-glass account on purpose. They skip steps 4 and 5, because a local
   record has no identity provider and no group values. Steps 1, 2, 6 and 7
   apply to them.
9. **A disable from the identity provider (PM).** For `ErrSubjectDisabled`,
   the service calls `SignInPrincipal` with `Create` false.
   - `ErrNotFound`: nothing to disable. The service writes the audit entry
     of the refused sign-in only. It never makes a principal only to
     disable it.
   - `ErrNotSignInTarget`: the service never disables the principal. This
     covers break-glass accounts, which no automatic step disables (ADR 70
     decision 25). It writes an audit entry.
   - A principal that it finds: the service disables it (ADR 70 decision
     19), with the bump of the session generation (ADR 297 decision 12),
     and writes the audit entry.
10. **The principal source of each method (PM).** ADR 70 decision 12 says
    that each record keeps its source. A person that the first sign-in
    makes has the source of its method: `oidc`, `oauth`, `saml`, `cas`,
    `ldap` or `header`. `principal.Source` adds `saml`, `cas` and `header`,
    and `SignInInput.Source` accepts `saml`, `cas`, `ldap` and `header`,
    each with the first adapter of the method. An LDAP person that a sign-in
    makes and an LDAP person that the sync makes have the same source,
    `ldap`.
11. **One answer, and the reason in the audit log (PM).** The browser
    receives one answer for each refused sign-in of a method. A password
    method uses the answer of ADR 285 decision 13: `401` and "The user name
    or the password is wrong.". The audit entry of the refusal records the
    real reason, the provider ID and the method kind. It never records a
    password, a ticket, a code or an assertion.

### Groups from a method

12. **A mapping turns group values into groups (PM, the rule waits for the
    owner).** The port `principal.GroupMapper` returns, for each group
    value, the IDs of zero or more groups in the home of the principal. The
    mapping is a stored setting of each source, and a sensitive setting
    (ADR 70 decision 22). It never comes from a name match that the server
    infers. The owner decides the form of the mapping. The port fits each
    form: an explicit map from a value to a group, or a match that a group
    turns on for one provider. Until the owner decides, no adapter of the
    mapper exists, and each source has no mapping.
13. **One function applies the group values (PM).**
    `principal.ApplyGroupClaims` applies the group values of one identity
    provider to the memberships of one principal, through the mapper. A
    sign-in calls it with the groups of the Assertion. A SCIM change of the
    groups of a user calls it too. The port is
    `principal.GroupClaimApplier`. Its rules:
    - **No mapping, no change.** When the source has no mapping, the call
      joins no group and changes no membership. It writes one audit entry,
      and the result lists each value as unmapped. This is the fail-closed
      rule.
    - It changes only the memberships of its source and source ID. It never
      changes a local membership, a membership of another provider or a
      pinned membership (ADR 70 decision 20).
    - `GroupsPresent` false changes nothing. An empty list that is present
      ends each membership of the source.
    - It never makes a group. A value that maps to no group goes in the list
      `Unmapped` of the result and in the audit entry.
    - It refuses a group ID of another home from the mapper. Defense in
      depth: the mapper never returns one, and this check stays so that a
      mapper of another form cannot skip it.
    - It refuses a principal that the identity provider cannot manage: a
      principal that is not a person, a break-glass account and a temporary
      administrator. Defense in depth: `SignInPrincipal` refuses these too,
      and this check stays so that a SCIM caller cannot skip it.
14. **The rules of ADR 70 apply as they are (owner, ADR 70).** A protected
    group refuses the add unless it trusts the identity provider or full
    sync is on (ADR 70 decisions 14, 16 and 17). A group that a person
    closed refuses the add (decision 15). A removal from a protected group
    suspends the membership, and full sync makes it final (decision 20). The
    removals count for the mass-suspension alert, with the limit of ADR 306
    decision 2. Each add, removal, suspension and refused add writes an
    audit entry in the transaction of the change.
15. **The source `ldap` (PM).** A membership from an LDAP directory, at a
    sign-in or at a sync, has the source `ldap` and the provider ID as its
    source ID. A group value of OIDC, OAuth 2.0, SAML, CAS or a trusted
    header has the source `claim` and the provider ID. SCIM keeps the source
    `scim`, with the SCIM connection as its source ID. The first LDAP
    adapter adds `ldap` to `principal.ParseMembershipSource` and to the
    store suites.

### Fresh authentication

16. **Each method asks for a fresh authentication in its own way (PM).** The
    fresh step of ADR 227 decision 1 uses the parameter of each protocol:

    | Method | How the server asks | What the server checks | Fresh kind |
    | --- | --- | --- | --- |
    | OIDC | `max_age=0` | `auth_time` in the ID token | `sso` |
    | SAML | `ForceAuthn="true"` | `AuthnInstant` of the `AuthnStatement` | `sso` |
    | CAS | `renew=true` on `/login` and on `/p3/serviceValidate` | a validation with `renew=true` succeeds, and the time of that validation is the authentication time | `sso` |
    | LDAP | a new bind with the password | the time of the bind, with the rule of decision 17 | `password` or `mfa` |
    | Plain OAuth 2.0 | none | none | ADR 301 |
    | Trusted header | none | none | ADR 301 |

    A fresh step of OIDC, SAML or CAS counts only when `AuthTimeKnown` is
    true and the authentication time is not before the start of the request
    that asked for it. The skew of the method applies. An authentication
    time in the future, after the skew, refuses the step. CAS 3.0 sends an
    authentication time only in the optional attribute `authenticationDate`.
    So the CAS method sets `AuthTimeKnown` for a validation with
    `renew=true`, which the protocol allows only for a ticket from a new
    primary sign-in. When the server also sends `authenticationDate`, the
    method checks that it is not before the start.
17. **LDAP is a password method (owner, ADR 285 decision 21).** For the
    fresh step, an LDAP person follows ADR 285 decision 21, as a local
    password user does. A password alone is a fresh authentication only for
    a person with no factor, in an installation with no SSO provider where
    MFA is not required. Else the fresh step is a password and a second
    factor (`mfa`), or a passkey.
18. **The fresh step belongs to the person of the session (PM).** A request
    for a fresh step keeps the principal ID and the sign-in ID of its
    session in the pending request. When the method returns, the service
    calls `session.CheckFreshSubject`. The Assertion must name the same
    person: the same subject `<provider ID>:<subject>` for a method port,
    and the same principal ID for the local password and the passkey.
    Otherwise the service refuses the step with `ErrFreshSubject`
    (`mfa.fresh-step-required` to the browser), writes an audit entry with
    both subjects, and changes no cookie. So a person cannot do the fresh
    step as another account at the identity provider.

### Sign-in failures

19. **LDAP counts the typed name and the person (PM).** The LDAP sign-in
    uses the counts of ADR 285 decision 14 and the source budget of
    decision 15, on two keys:
    - **The typed name.** The service adds a failure to the counts of the
      folded name that the person typed (`password` and `passwordname`)
      before it calls `Verify`. This count stops a guess against many
      names, including names that do not exist.
    - **The person.** The method calls `BindGate.BeforeBind` with the
      subject that the search found, before it sends the password. The
      service adds a failure to the counts of that subject, in the same two
      scopes. So every form of one user name (for example `alice`,
      `ALICE`, `alice@corp` and `CORP\alice` in Active Directory) counts
      on one key, and the global lock of 100 failures holds for the person.
      A lock on the subject stops the bind, so no password reaches the
      directory while the lock holds.

    The name of a count is the keyed digest of the provider ID, the kind of
    key (`name` or `subject`) and the value. So the counts of one user name
    at two providers stay apart, and an unknown name locks the same as a
    known one. The service clears the counts only after the commit of a
    full sign-in. A directory that does not answer leaves the counts.
20. **The directory lockout (PM).** Each bind that OpenDustpan lets through
    counts in the lockout policy of the directory too. So an attacker who
    tries passwords through OpenDustpan can lock the account of the person
    in the whole organization. The setting `ldap.subjectFailureLimit` sets
    the limit of the count of the subject from every source: 5 by default,
    1 to 100. The guide tells the operator to set it below the threshold of
    the directory, and says that a lock in OpenDustpan then comes first.

    The lock times follow ADR 285 decision 14:
    - The count of the subject from one source prefix locks the person from
      that prefix for 30 minutes after 10 failures in a row.
    - The count of the subject from every source locks the person for every
      source after `ldap.subjectFailureLimit` failures in a row. The first
      lock lasts 30 minutes. Each new lock of the same person doubles the
      time, up to 24 hours. After 24 hours with no lock, the next lock lasts
      30 minutes again.

    The risk that stays: anyone who knows the user name of an LDAP person
    can lock that person out of the LDAP sign-in from one network, with 5
    wrong passwords by default, and lock the person again after each lock
    ends. The ways out are an administrator unlock through the routes of
    decision 21, and the alerts of ADR 285 decision 16, which tell the
    administrators. This decision takes that cost to protect the account of
    the person in the directory. The guide says so.
21. **An LDAP lock is visible and ends with a record (owner, ADR 285
    decision 16).** An administrator reads and clears the locks of an LDAP
    person with `GET /api/dustpan/v1/sign-in/providers/{providerID}/locks?principalId=...`
    and `POST /api/dustpan/v1/sign-in/providers/{providerID}/unlock`. The
    unlock takes the principal ID, an optional typed user name, and a
    reason. The routes need the action of the local user lock routes
    (`server:manage-sign-in` in code today) and a reach that covers the reach
    of the person, as those routes do. The unlock
    clears the subject counts of the person, and the counts of the typed
    name when the request names one. Each read of another person and each
    unlock writes an audit entry, and the lock alerts of ADR 285 decision 16
    apply.
22. **A passkey sign-in counts by client address (PM).** A discoverable
    passkey sends no user name, and the client chooses the credential ID. A
    count by credential ID would let anyone who knows the ID lock the person
    out. So the passkey sign-in counts the failed assertions of each client
    address (an IPv4 address, or the /64 of an IPv6 address) in the source
    budget, in its own scope `passkey`. 20 failures in 15 minutes refuse
    that address for 15 minutes. A passkey cannot be guessed, so this limit
    stops load, not guesses. The passkey sign-in reads no name lock and no
    factor lock (ADR 285 decision 14).
23. **A break-glass account has no lockout by name (PM).** It signs in with
    a passkey or a hardware key only (ADR 70 decision 23), so the counts of
    decision 19 do not apply to it. The limit by address of decision 22
    applies. At each 10 failed assertions for a credential of a break-glass
    account, the server sends an alert to each site administrator, at most
    once for each account in each hour, as ADR 285 decision 16 limits its
    alerts. So no "blocked" state exists that the guard of ADR 70 decision
    24 must count.
24. **A redirect method and a request method have no lockout by name (PM).**
    The identity provider or the proxy checks the password. The routes of
    the callback keep the rate limits of the server.

### Providers

25. **Each mode has its own settings (PM).** `session.ProviderMode` adds
    `saml`, `cas`, `ldap` and `header`, each with its own settings struct in
    `ProviderConfig`, as `OAuthSettings` is today. The provider admin routes
    of today serve each mode. Each adapter adds its mode in its own change.

## Not decided here

- **The form of the group mapping (decision 12).** The owner decides it. A
  method of ADR 321 that needs groups ships with no mapping until then, and
  its sign-in joins no group.
- The design of each method: ADR 321.
- SCIM, built-in MFA enrollment, WebAuthn, TOTP, recovery codes, IP
  allowlists, mTLS, the token expiry email and break-glass mode 2. RFC 21
  P7 to P12 and their later ADRs hold them.

## Consequences

- RFC 21 work item W2 becomes the implementation of these ports, the
  `ApplyGroupClaims` service and the order of decision 7. Each method of ADR
  321 then needs only its adapter, its settings and its e2e step.
- W2 also needs this data, in both dialects:
  - a home column on `principal_groups`, and a unique key of the home and
    the folded name in place of the key on the name alone,
  - the trust of a group (ADR 70 decision 16) and its close switch
    (decision 15),
  - the full-sync setting of the installation (decision 17),
  - the store of the group mapping, after the owner decides its form.

  ADR 70 decision 13 computes the protection of a group from its grants, so
  no column stores it.
- The OIDC and plain OAuth 2.0 sign-in of today move behind
  `RedirectMethod` in W2. Their behavior does not change, except the groups
  claim of ADR 321.
- `session.Assertion`, `principal.GroupClaims` and the mapper are new types.
  No data changes, because no tag shipped them.
- `principal.Source` and `principal.ParseMembershipSource` grow with the
  first adapter of each method. The conformance suite of the principal
  store then accepts the new values.
- The settings resource adds `ldap.subjectFailureLimit`. A higher limit is a
  weaker setting (ADR 212 decision 3).
