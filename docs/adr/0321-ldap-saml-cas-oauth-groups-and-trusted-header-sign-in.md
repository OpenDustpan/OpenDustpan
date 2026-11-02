# 321. LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in

Status: accepted · 2026-11-02. The design is the proposals P2 to P6 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md), with decisions of the PM under the owner's delegation. The owner accepts them with the merge. The owner decisions that it uses are in ADR 55, ADR 285 (the modules), ADR 292 and ADR 301. The start of a SAML sign-in (RFC 21 question O4) is not decided here

Changes:

- Extends ADR 55 decision 1: the design of the LDAP, SAML and CAS adapters.
- Extends ADR 55 decision 6: two modes of the trusted header, each with two layers, and groups from a header.
- Extends ADR 55 decision 7: a groups claim for a plain OAuth 2.0 provider, and the egress rules on its user-info URL.
- Extends ADR 55 decision 9: the server finds an LDAP removal at each sign-in and with an hourly sync.

## Context

ADR 55 puts LDAP, SAML, CAS, any OAuth 2.0 provider and the trusted header
in the binary. ADR 320 gives the one sign-in path, the ports and the rules
for groups. RFC 21 P2 to P6 propose the design of each method. ADR 285
records that the owner approved the modules `github.com/go-ldap/ldap/v3`,
`github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig`
and `github.com/beevik/etree`. ADR 292 refuses a plain LDAP connection unless
an operator opts in. ADR 301 gives the fresh step of a plain OAuth 2.0
provider and of a trusted header.

RFC 21 left these questions open, and this ADR answers them:

- How does the server find that LDAP removed a user (ADR 55, "Not decided
  here")?
- Does trusted-header sign-in give groups as well as a user (ADR 55, "Not
  decided here")?
- A plain header, a signed header, or both (RFC 21 P6)?
- How does SAML ask for a fresh authentication (RFC 21 P3 question 2)?

Each fact about another product is from RFC 21, with its mark there.

## Decision

### LDAP

1. **The check.** The adapter binds with a service account and searches for
   the user with a filter that the administrator sets. It then binds as the
   user that the search found, to check the password. It never reads or
   compares a password hash. A search that finds two users refuses the
   sign-in.
2. **The boundary and the encoding.** A user name has 1 to 256 bytes and no
   control character. Each value in a filter passes through the filter
   escape of RFC 4515. Each value in a DN passes through the DN escape of
   RFC 4514.
3. **The subject.** The subject is `entryUUID` (RFC 4530) by default,
   `objectGUID` for Active Directory, or an attribute that the administrator
   names. It is never the DN, the `uid` or the email, because they can
   change (ADR 70 decision 22).
4. **Groups.** A group search or `memberOf`, by a setting. The adapter
   returns each group that the user reaches through nested groups, as a
   flat list (RFC 19). The groups go through `principal.ApplyGroupClaims`
   with the source `ldap` (ADR 320 decision 12).
5. **TLS.** `ldaps://` or StartTLS, with certificate checks and an optional
   CA bundle. A connection with no TLS follows ADR 292.
6. **The service account password** is a sensitive setting. It comes only
   from an environment variable or a `*_FILE` file (ADR 212 decision 2).
7. **Limits.** 10 seconds for a connection, 10 seconds for an operation, at
   most 1000 entries for a search of groups. A size limit of the directory
   answer refuses the sign-in. It is never a partial result.
8. **Removals: at each sign-in and with a sync.** Precedent: GitLab checks
   each LDAP user in a daily worker, and syncs groups each hour (fetched).
   Keycloak finds deletions only with a full sync (from a search summary).
   - Each sign-in binds as the user. A user that the directory reports as
     disabled is a termination (ADR 70 decision 19). The attribute is a
     setting, for example bit 2 of `userAccountControl` for Active
     Directory. The method returns `ErrSubjectDisabled`.
   - A durable operation (`pkg/durable`) syncs the directory at the interval
     `ldap.syncIntervalSeconds`: 3600 by default, at least 300. It runs a
     full search, because a search of changed entries does not find
     deletions. A user that the search does not find, or that the directory
     reports as disabled, is a termination. A longer interval is a weaker
     setting (ADR 212 decision 3).
   - An unreachable directory refuses the sign-in. For the sync, an
     unreachable directory changes no user and raises an alert. It never
     disables every user, because a network fault is not a termination.
   - A sync that disables more users than the limit of the mass-suspension
     alert (ADR 306 decision 2) still applies each termination and raises
     the alert.
9. **Fresh authentication.** A new bind with the password (ADR 320 decisions
   13 and 14).

### SAML

10. **Bindings.** HTTP-Redirect for the request, HTTP-POST for the
    response.
11. **The checks of the response.** The server refuses an unsigned
    assertion. It accepts a signed response only when the response holds
    exactly one assertion. It refuses SHA-1 and each signature algorithm
    outside an allowlist: RSA-SHA256 and stronger, ECDSA-SHA256 and
    stronger. It checks `Destination`, `Recipient`, `Audience`,
    `InResponseTo`, `NotBefore` and `NotOnOrAfter`, with at most 120 seconds
    of clock skew.
12. **Replay.** The server stores each assertion ID until its
    `NotOnOrAfter`, and refuses a second use with `ErrReplay`.
13. **The parser.** The body has at most 256 KiB before the decode. The XML
    parser refuses a DTD, an entity and a depth of more than 64.
14. **Encrypted assertions** are optional, with a key of the installation
    that the secret package seals.
15. **The subject.** A persistent `NameID` by default, or an attribute that
    the administrator names. The server refuses a transient or email
    `NameID` as a subject, because it changes.
16. **Groups** come from an attribute that the administrator names, with the
    source `claim`.
17. **Metadata.** The metadata of the identity provider comes from a URL,
    through the egress rules of `pkg/egress`, or from an upload. The server
    pins the signing certificates, and a change of them is a sensitive change
    with an alert (ADR 70 decision 22). The server publishes its own service
    provider metadata at a public route.
18. **Library.** `github.com/russellhaering/gosaml2` with `goxmldsig` and
    `etree`. The owner approved them (ADR 285, context). Both Go SAML
    libraries had signature bypass bugs (OSV). So the server checks the count
    of assertions, the algorithm allowlist and the replay itself, as if the
    library failed. Each of these checks has the comment of CLAUDE.md
    for a defense-in-depth layer.
19. **Fresh authentication.** `ForceAuthn="true"` and a check of
    `AuthnInstant` (ADR 320 decision 13).
20. **No single logout.** ADR 55 does not list it. A console session ends
    after 12 hours at most (ADR 69).

### CAS

21. **The protocol.** CAS protocol 3.0. The server sends the browser to
    `<cas>/login?service=...` and checks the ticket with a server-to-server
    call to `<cas>/p3/serviceValidate`, over HTTPS only, through the egress
    rules.
22. **The service URL** comes from the configured external URL of the
    installation and the provider ID. It never comes from the request.
23. **The ticket.** It starts with `ST-`, has at most 256 characters, and
    holds only `A-Za-z0-9-._`. The server uses a ticket once, and refuses a
    second use with `ErrReplay`.
24. **No proxy tickets.** The server never sends `pgtUrl` and accepts no
    proxy ticket.
25. **The answer** has at most 64 KiB of XML, with no DTD.
26. **The subject** is an attribute that the administrator names. With no
    attribute, the subject is the CAS `user`, and the provider page says
    that a rename in the CAS server makes a new principal.
27. **Groups** come from an attribute that the administrator names, with the
    source `claim`.
28. **Fresh authentication.** `renew=true` (ADR 320 decision 13).
29. **No new module.** `net/http` and `encoding/xml` handle the protocol in
    the adapter.

### OAuth 2.0

30. **A groups claim.** `OAuthSettings` has a new field `GroupsClaim`: the name of a
    claim in the user-info answer that holds a JSON array of strings. The
    limits of ADR 320 decision 3 apply. A claim of another type refuses the
    sign-in with `ErrMethodAnswer`. The groups go through
    `principal.ApplyGroupClaims` with the source `claim`. Only the user-info
    answer counts. A provider that needs more API calls for groups, for
    example the teams of a Git host, is not in scope.
31. **The egress rules.** The user-info URL goes through the egress rules of
    `pkg/egress`, so a provider setting cannot reach an address that the
    egress rules refuse.
32. **Fresh authentication.** None from the provider. ADR 301 applies.

### Trusted header

33. **Two modes, and each mode has two layers.** A plain header from a
    trusted address alone is one layer, which CLAUDE.md refuses. Precedent:
    Google IAP and the AWS Application Load Balancer send a signed JWT and
    tell a service to check it (fetched).
    - **`jwt` mode.** The server checks the signature of the JWT against the
      keys of the issuer (a JWKS URL, or the key URL pattern of the load
      balancer). It checks the issuer, the audience, `exp` and `iat`, with at
      most 60 seconds of skew. The peer must also be in `--trusted-proxies`.
      Presets for Google IAP and AWS ALB fill the header name, the key URL and
      the issuer.
    - **`plain` mode.** The peer must be in `--trusted-proxies`, and the
      request must also carry a second proof of the proxy: a shared secret
      header that the server compares in constant time, or a TLS client
      certificate of the proxy that a trust bundle checks. The shared secret
      comes first. The client certificate proof comes with mTLS (RFC 21
      P10).
34. **Removal of the headers.** The server removes the user and groups
    headers of a request from any other peer before a handler reads it, and
    writes an audit entry for a refused header.
35. **Groups from a header.** Off by default. The operator names a second
    header, or a claim in `jwt` mode. The groups go through
    `principal.ApplyGroupClaims` with the source `claim`. An empty header is
    an empty list. A missing header, with groups on, refuses the sign-in with
    `ErrGroupsMissing`, because a proxy that dropped the header must not
    remove every membership. Precedent: Grafana reads groups from a header
    that the operator names (fetched).
36. **The subject.** The `sub` claim in `jwt` mode. The user header in
    `plain` mode, with the allowlist of characters of a subject at the
    boundary.
37. **Fresh authentication.** None from the proxy. ADR 301 applies.

## Not decided here

- **The start of a SAML sign-in (RFC 21 question O4).** The owner has not
  answered whether the server refuses a sign-in that the identity provider
  starts. Until the owner answers, the port refuses a callback with no
  pending request (`ErrRequestBinding`), which is the fail-closed reading.
  The SAML adapter waits for the answer.
- SCIM and the pairing of a SCIM connection with a sign-in provider (RFC 21
  P8).
- The proof of the proxy with a client certificate, and its trust bundle
  (RFC 21 P10).

## Consequences

- RFC 21 work items W9 (LDAP), W11 (SAML), W12 (CAS), W13 (OAuth 2.0
  groups) and W14 (trusted header) implement this ADR. W10 implements the
  LDAP sync.
- SAML and CAS each add a table of used values (assertion IDs and tickets),
  in both dialects.
- Each method has an e2e step with an in-process test server before it
  ships (ADR 55). RFC 21 lists the steps.
- The settings resource adds `ldap.syncIntervalSeconds`. A longer interval
  is a weaker setting.
