# 321. LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in

Status: accepted · 2026-11-02. The design is the proposals P2 to P6 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md), with decisions of the PM under the owner's delegation, marked "(PM)". The owner accepts them with the merge. A decision marked "(owner, ADR N)" repeats an owner decision of that ADR. The start of a SAML sign-in (RFC 21 question O4) and the form of the group mapping (ADR 320 decision 12) wait for the owner

Changes:

- Extends ADR 55 decision 1: the design of the LDAP, SAML and CAS adapters.
- Extends ADR 55 decision 6: two modes of the trusted header with two layers each. Each provider has its own list of proxies. Groups can come from a header.
- Extends ADR 55 decision 7: a groups claim for a plain OAuth 2.0 provider. Each outbound connection of a sign-in method uses the egress rules.
- Extends ADR 55 decision 9: the server finds an LDAP removal at each sign-in and with an hourly sync.
- Changes ADR 70 decision 19: an LDAP sync with no entry, or above a bound, disables nobody. A site administrator decides.

## Context

ADR 55 puts LDAP, SAML, CAS, any OAuth 2.0 provider and the trusted header
in the binary. ADR 320 gives the one sign-in path, the ports, the group
mapping port and the rules for groups. RFC 21 P2 to P6 propose the design of
each method. ADR 285 records that the owner approved the modules
`github.com/go-ldap/ldap/v3`, `github.com/russellhaering/gosaml2`,
`github.com/russellhaering/goxmldsig` and `github.com/beevik/etree`. ADR 292
refuses a plain LDAP connection unless an operator opts in. ADR 301 gives the
fresh step of a plain OAuth 2.0 provider and of a trusted header. ADR 63
decisions 4 and 6 say that a secret comes only from an environment variable
or a `*_FILE` file, and that a log or an audit entry never shows a sensitive
setting.

RFC 21 left these questions open, and this ADR answers them:

- How does the server find that LDAP removed a user (ADR 55, "Not decided
  here")?
- Does trusted-header sign-in give groups as well as a user (ADR 55, "Not
  decided here")?
- A plain header, a signed header, or both (RFC 21 P6)?
- How does SAML ask for a fresh authentication (RFC 21 P3 question 2)?

RFC 21 marks the first two "for the owner to decide". Its proposal section
gives them to the PM under the rule of that section: a clear precedent, and
no persona loses. This ADR marks them "(PM)".

Each fact about another product is from RFC 21, with its mark there.

## Decision

### Outbound connections

1. **Each outbound connection of a sign-in method goes through the egress
   rules (PM).** The rules of `pkg/egress` and the client of
   `internal/egress/egresshttp` apply to each connection that a method
   opens, with a check of the resolved IP address when the connection
   opens:
   - OIDC discovery, the token endpoint, the JWKS URL and the user-info URL
     (OIDC and plain OAuth 2.0). `internal/oauth` moves to this client.
   - The SAML metadata URL.
   - The CAS validation URL.
   - The key URL of the `jwt` mode of the trusted header.
   - The LDAP connection. The dialer of the LDAP client checks the resolved
     address with the same rules.

   So a provider setting cannot reach an address that the egress rules
   refuse.

### LDAP

2. **The check (PM).** The adapter binds with a service account and
   searches for the user with a filter that the administrator sets. It
   calls `BindGate.BeforeBind` with the subject that it found (ADR 320
   decision 19). It then binds as that user to check the password. It never
   reads or compares a password hash. A search that finds two users refuses
   the sign-in.
3. **The boundary and the encoding (PM).** A user name has 1 to 256 bytes
   and no control character. An empty password is refused with
   `ErrMethodInput` before any bind, because an empty password makes an
   unauthenticated bind (RFC 4513 section 5.1.2), which many directories
   accept. Each value in a filter passes through the filter escape of RFC
   4515. Each value in a DN passes through the DN escape of RFC 4514.
4. **No referrals (PM).** The adapter never follows a referral or a search
   continuation reference. A referral can send the password to another
   server. An answer that holds only a referral refuses the sign-in with
   `ErrMethodAnswer`.
5. **The subject (PM).** The subject is `entryUUID` (RFC 4530) by default,
   `objectGUID` for Active Directory, or an attribute that the administrator
   names. It is never the DN, the `uid` or the email, because they can
   change (ADR 70 decision 22). The text form is fixed, because the server
   stores the subject:
   - `entryUUID`: the UUID text of RFC 4530, in lowercase.
   - `objectGUID`: the 16 bytes as the GUID text that Active Directory
     shows, in lowercase: the first three groups from the bytes in
     little-endian order, the last two groups in the order of the bytes. For
     the bytes `01 02 03 04 05 06 07 08 09 0a 0b 0c 0d 0e 0f 10`, the text is
     `04030201-0605-0807-090a-0b0c0d0e0f10`.
   - An attribute that the administrator names: its value as a string,
     with the limits of a subject. A binary value refuses the sign-in.
6. **Groups (PM).** A group search or `memberOf`, by a setting. The adapter
   returns each group that the user reaches through nested groups, as a
   flat list (RFC 19). The groups go through `principal.ApplyGroupClaims`
   with the source `ldap` and the group mapping of ADR 320 decision 12.
7. **TLS (owner, ADR 292).** `ldaps://` or StartTLS, with certificate checks
   and an optional CA bundle. A connection with no TLS follows ADR 292.
8. **The service account password (owner, ADR 63).** It is a sensitive
   setting. It comes only from an environment variable or a `*_FILE` file
   (ADR 63 decision 4), and no log or audit entry shows it (decision 6).
9. **Limits (PM).** 10 seconds for a connection, 10 seconds for an
   operation, at most 1000 entries for a search of groups. A size limit of
   the directory answer refuses the sign-in. It is never a partial result.
10. **A removal at each sign-in (PM).** Precedent: GitLab blocks a user that
    fails the LDAP check at sign-in (fetched). Each sign-in searches for the
    user and binds as the user. A user that the directory reports as
    disabled is a termination (ADR 70 decision 19), for that one person. The
    attribute is a setting, for example bit 2 of `userAccountControl` for
    Active Directory. The method returns `ErrSubjectDisabled`, and the
    service follows ADR 320 decision 9. A user that the search does not
    find receives `ErrMethodRejected`: the sign-in fails closed for that person,
    and the principal stays as it is until the sync.
11. **A removal with a sync, with a bound (PM).** Precedent: GitLab checks
    each LDAP user in a daily worker, and syncs groups each hour (fetched).
    Keycloak finds deletions only with a full sync (from a search summary).
    - A durable operation (`pkg/durable`) syncs the directory at the
      interval `ldap.syncIntervalSeconds`: 3600 by default, at least 300. It
      runs a full search, because a search of changed entries does not find
      deletions. A longer interval is a weaker setting (ADR 212 decision 3).
    - A person whose entry the search does not find, or that the directory
      reports as disabled, is a termination candidate.
    - **The bound.** A run that finds no entry at all, or that has more
      termination candidates than the limit of the mass-suspension alert
      (ADR 306 decision 2: 10 persons, or 20 percent of the persons of the
      provider), disables nobody. It raises the alert and stores the list of
      the candidates. A site administrator reads the list and approves it
      with a fresh authentication and a reason. Then the server applies each
      termination of the list that is still a candidate. Each approval writes
      an audit entry. A wrong base DN, a filter change or a change of the
      rights of the service account can return a search with few or no
      entries. This bound stops such a search from disabling every LDAP
      person.
    - A run under the bound applies each termination at once, as ADR 70
      decision 19 says.
    - An entry that the run cannot read, for example one with no subject
      attribute, is not a termination. The run skips it and raises an alert.
    - An unreachable directory changes no person and raises an alert. A
      network fault is not a termination.
    - The base DN, the user filter and the subject attribute are sensitive
      settings with an alert (ADR 70 decision 22).

    This changes ADR 70 decision 19 for a sync above the bound only. A
    termination at a sign-in (decision 10) still applies at once.
12. **Fresh authentication (PM).** A new bind with the password (ADR 320
    decisions 16 and 17).

### SAML

13. **Bindings (PM).** HTTP-Redirect for the request, HTTP-POST for the
    response.
14. **The checks of the response (PM).** Both Go SAML libraries had
    signature bypass bugs (OSV), and most of them were signature wrapping.
    So the adapter does these checks itself, as well as the library:
    - It counts each `Assertion` and each `EncryptedAssertion` element
      anywhere in the document, at any depth. The count must be exactly 1.
    - It reads each value only from the signed, canonical element that the
      library returns after the signature check. It never reads a value from
      the original tree.
    - The `URI` of the signature `Reference` must equal the `ID` of that
      signed element.
    - The signature algorithm is on an allowlist: RSA-SHA256 and stronger,
      ECDSA-SHA256 and stronger. The digest algorithm is on an allowlist:
      SHA-256 and stronger. SHA-1 is refused for both.
    - An unsigned assertion is refused.
    - It checks `Destination`, `Recipient`, `Audience`, `InResponseTo`,
      `NotBefore` and `NotOnOrAfter`, with at most 120 seconds of clock
      skew.

    Each of these checks has the comment of CLAUDE.md for a defense-in-depth
    layer.
15. **Replay (PM).** The server stores each assertion ID until its
    `NotOnOrAfter`, and refuses a second use with `ErrReplay`. An assertion
    with no `NotOnOrAfter`, or with a `NotOnOrAfter` more than 1 hour after
    the receipt, is refused. So no replay record lives longer than 1 hour
    and the skew.
16. **The parser (PM).** The body has at most 256 KiB before the decode.
    The XML parser refuses a DTD, an entity and a depth of more than 64.
17. **Encrypted assertions (PM)** are optional, with a key of the
    installation that the secret package seals.
18. **The subject (PM).** A persistent `NameID` by default, or an attribute
    that the administrator names. The server refuses a transient or email
    `NameID` as a subject, because it changes.
19. **Groups (PM)** come from an attribute that the administrator names,
    with the source `claim` and the group mapping of ADR 320 decision 12.
20. **Metadata and signing certificates (PM).** The metadata of the identity
    provider comes from a URL, through the egress rules, or from an upload.
    The server pins the signing certificates that a site administrator
    accepted. A certificate that is new in fetched metadata does not take
    effect: the server stores it as pending, raises an alert, and keeps the
    accepted certificates. A site administrator accepts it with a fresh
    authentication and a reason (ADR 70 decision 22). An upload is the
    acceptance, with the same checks. The server publishes its own service
    provider metadata at a public route.
21. **Library (owner, ADR 285).** `github.com/russellhaering/gosaml2` with
    `goxmldsig` and `etree`. The owner approved them. The checks of
    decision 14 hold as if the library failed.
22. **Fresh authentication (PM).** `ForceAuthn="true"` and a check of
    `AuthnInstant` (ADR 320 decision 16).
23. **No single logout (PM).** ADR 55 does not list it. A console session
    ends after 12 hours at most (ADR 69).

### CAS

24. **The protocol (PM).** CAS protocol 3.0. The server sends the browser to
    `<cas>/login?service=...` and checks the ticket with a server-to-server
    call to `<cas>/p3/serviceValidate`, over HTTPS only, through the egress
    rules.
25. **The service URL (PM)** comes from the configured external URL of the
    installation and the provider ID. It never comes from the request.
26. **The ticket (PM).** It starts with `ST-`, has at most 256 characters,
    and holds only `A-Za-z0-9-._`. The server uses a ticket once, and
    refuses a second use with `ErrReplay`. A used ticket record lives 1 hour.
27. **No proxy tickets (PM).** The server never sends `pgtUrl` and accepts no
    proxy ticket.
28. **The answer (PM)** has at most 64 KiB of XML, with no DTD.
29. **The subject (PM)** is an attribute that the administrator names. With
    no attribute, the subject is the CAS `user`, and the provider page says
    that a rename in the CAS server makes a new principal.
30. **Groups (PM)** come from an attribute that the administrator names,
    with the source `claim` and the group mapping of ADR 320 decision 12.
31. **Fresh authentication (PM).** `renew=true` on both calls. A validation
    with `renew=true` sets the authentication time (ADR 320 decision 16).
32. **No new module (PM).** `net/http` and `encoding/xml` handle the
    protocol in the adapter.

### OAuth 2.0

33. **A groups claim (PM).** `OAuthSettings` has a new field `GroupsClaim`:
    the name of a claim in the user-info answer that holds a JSON array of
    strings. The limits of ADR 320 decision 3 apply. A claim of another type
    refuses the sign-in with `ErrMethodAnswer`. The groups go through
    `principal.ApplyGroupClaims` with the source `claim` and the group
    mapping of ADR 320 decision 12. Only the user-info answer counts. A
    provider that needs more API calls for groups, for example the teams of
    a Git host, is not in scope.
34. **Fresh authentication (owner, ADR 301).** None from the provider. ADR
    301 applies.

### Trusted header

35. **Each header provider has its own list of proxies (PM).** The provider
    setting `header.trustedProxies` is a list of addresses and CIDR ranges.
    It is separate from `--trusted-proxies`, which only tells the server how
    to read the client address (ADR 212 decision 4). A load balancer that
    forwards the client address is not always the proxy that authenticates
    a person. The peer of a header sign-in must be in the list of its
    provider. The list is a sensitive setting with an alert (ADR 70 decision
    22). An empty list turns the provider off.
36. **Two modes, and each mode has two layers (PM).** A plain header from a
    trusted address alone is one layer, which CLAUDE.md refuses. Precedent:
    Google IAP and the AWS Application Load Balancer send a signed JWT and
    tell a service to check it (fetched).
    - **`jwt` mode.** The peer must be in the list of decision 35. The
      server checks the signature of the JWT against the keys of the issuer
      (a JWKS URL, or the key URL pattern of the load balancer). It checks
      the issuer, the audience, `exp` and `iat`, with at most 60 seconds of
      skew. The algorithm is on an allowlist: ES256 for the presets, and
      ES256, ES384, RS256 and stronger, and EdDSA for a custom issuer. `none`
      and each HMAC algorithm are refused. Presets for Google IAP and AWS ALB
      fill the header name, the key URL and the issuer.
    - **AWS ALB.** The key ID (`kid`) must match `^[A-Za-z0-9-]{1,128}$`
      before the server puts it in the key URL. The `signer` field must equal
      the ARN of the load balancer that the operator sets.
    - **`plain` mode.** The peer must be in the list of decision 35, and the
      request must also carry a proxy proof: a shared secret header that the
      server compares in constant time, or a TLS client certificate of the
      proxy that a trust bundle checks. The shared secret comes first. The
      client certificate proof comes with mTLS (RFC 21 P10). The shared
      secret is a sensitive setting (ADR 63 decisions 4 and 6).
37. **Removal of the headers (PM).** The server removes the user and groups
    headers of a request from any peer outside the list of decision 35
    before a handler reads it, and writes an audit entry for a refused
    header.
38. **Groups from a header (PM).** Off by default. The operator names a
    second header, or a claim in `jwt` mode. The groups go through
    `principal.ApplyGroupClaims` with the source `claim` and the group
    mapping of ADR 320 decision 12. An empty header is an empty list. A
    missing header, with groups on, refuses the sign-in with
    `ErrGroupsMissing`, because a proxy that dropped the header must not
    remove every membership. Precedent: Grafana reads groups from a header
    that the operator names (fetched).
39. **The subject (PM).** The `sub` claim in `jwt` mode. The user header in
    `plain` mode, with the allowlist of characters of a subject at the
    boundary.
40. **Fresh authentication (owner, ADR 301).** None from the proxy. ADR 301
    applies.

## Not decided here

- **The start of a SAML sign-in (RFC 21 question O4).** The owner has not
  answered whether the server refuses a sign-in that the identity provider
  starts. Until the owner answers, the port refuses a callback with no
  pending request (`ErrRequestBinding`), which is the fail-closed reading.
  The SAML adapter waits for the answer.
- **The form of the group mapping (ADR 320 decision 12).** Until the owner
  decides, the sign-in of each method joins no group.
- SCIM and the pairing of a SCIM connection with a sign-in provider (RFC 21
  P8).
- The proof of the proxy with a client certificate, and its trust bundle
  (RFC 21 P10).

## Consequences

- RFC 21 work items W9 (LDAP), W11 (SAML), W12 (CAS), W13 (OAuth 2.0
  groups) and W14 (trusted header) implement this ADR. W10 implements the
  LDAP sync with its bound and its approval route.
- SAML and CAS each add a table of used values (assertion IDs and tickets),
  in both dialects. SAML adds the pending and accepted signing
  certificates. The LDAP sync adds the stored list of candidates.
- `internal/oauth` moves to the egress client before the groups claim
  ships.
- Each method has an e2e step with an in-process test server before it
  ships (ADR 55). RFC 21 lists the steps.
- The settings resource adds `ldap.syncIntervalSeconds`. A longer interval
  is a weaker setting.
