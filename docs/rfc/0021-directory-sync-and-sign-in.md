---
rfc: 21
title: Directory sync and sign-in hardening
status: discussing
created: 2026-10-10
adr: 46, 55, 56, 57
---

# RFC 21. Directory sync and sign-in hardening

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the built-in MFA below on 2026-10-11.

The part on built-in MFA is accepted as ADR 46. The sign-in methods for
milestone 4 are accepted as ADR 55, MFA freshness as ADR 56 and break-glass
recovery as ADR 57. The questions in "Open questions" stay open. The section
"Proposal for the rest of milestone 4 (2026-10-30)" answers them, with the
other identity items of milestone 4, for the owner to accept.

## Problem

Large organizations remove access through their identity provider. Spacelift,
env zero, HCP Terraform and Pulumi Cloud have SCIM provisioning. HCP Terraform
and env zero have IP allowlists. Pulumi Cloud has passkeys. OTF accepts
identity from a trusted proxy (Google IAP). ADR 6 covers sign-in adapters
only.

## First ideas

- SCIM 2.0 for users and groups, feeding teams (RFC 19).
- Passkeys for the local sign-in adapter.
- Trusted-header sign-in behind an identity-aware proxy, as an Authenticator
  adapter.
- IP allowlists, though self-hosters can also do this at their own proxy.
- Built-in MFA for installs that have no identity provider (see Proposal).

## Proposal

### Built-in MFA for installs with no identity provider

The project owner chose this MFA. It protects installs that sign people in
without an identity provider (for example, local users or LDAP).

- WebAuthn (passkeys, Touch ID, Windows Hello and hardware keys) and TOTP are
  options of the same rank. Recovery codes are shown once at enrollment.
- MFA is off by default, for any number of users. An administrator turns it
  on. Then the administrator can require it for all users.
- MFA covers console sign-in, the creation of access tokens, and the reveal
  of a secret value (ADR 41). An administrator can require MFA for a secret
  reveal with a separate setting.
- CLI and CI access tokens stay bearer tokens, with no MFA step on each use.
  A CLI access token of a person is created after an MFA sign-in, and it has
  an expiry (RFC 20).
- An administrator can reset the MFA of a user. Each reset writes an audit
  entry (ADR 41).
- With an SSO identity provider (ADR 6), MFA is the job of the identity
  provider. OpenDustpan does not add its own MFA step on top.
- Each MFA operation has a REST API and an action (ADR 17).

## Proposal for the rest of milestone 4 (2026-10-30)

Status of this section: a proposal, ready to accept as ADRs. It decides
nothing. The owner direction is that every milestone 4 item ships as planned,
with no scope cuts, and that nothing needs to migrate, because OpenDustpan has
no users yet.

This section covers the identity items of milestone 4 that have no accepted
design yet:

- the sign-in methods LDAP, SAML, CAS, OAuth 2.0 and trusted header, and
  passkeys (ADR 55),
- built-in MFA (ADR 46),
- SCIM 2.0 (ADR 55),
- the installation and token IP allowlists (ADR 55 decision 8),
- mTLS (ADR 54 decision 4, with [RFC 20](0020-access-tokens.md)),
- the token expiry email (ADR 53 decision 11, with RFC 20),
- break-glass mode 2 (ADR 57, ADR 245).

It does not reopen an accepted decision. Where an accepted ADR answers a
question of "Open questions" above, the subsection says so and names the ADR.

**Precedent marks.** Each fact about another product or a standard has a
mark. "(fetched)" means that the author read the source page on 2026-10-30.
"(not fetched)" means that the fact comes from memory or from a secondary
source, and a reviewer must check it before an ADR quotes it. PCI DSS and
ISO/IEC texts are behind a paywall. Their numbers are as the research note
`docs/research/m4-identity-defaults.md` and ADR 70 quote them. This proposal
never says that OpenDustpan complies with a standard. It says "designed
against".

**Who decides.** Each question has a recommendation. The PM decides a
question under its rule only when a clear precedent exists, the decision
upsets no persona, and it is not stupid on a step back. Else the question has
the mark "owner decision". The list "Questions for the owner" holds each
owner decision as a yes or no question.

### Facts on `main` (checked 2026-10-30, at 252b87f)

- `identity.Authenticator` has one method, `AuthenticateToken`
  (`pkg/identity/identity.go`). The adapters are the access token service, the
  temporary administrator decorator, the sign-in failure gate and the
  principal guards. No adapter exists for LDAP, SAML, CAS, a header or mTLS.
- Browser sign-in exists for OIDC and for plain OAuth 2.0.
  `session.ProviderMode` has `oidc` and `oauth`. `session.OAuthSettings` holds
  the authorization, token and user-info URLs and the claim mapping. The
  server pins the issuer of a provider (`session.BindProviders`,
  `ErrIssuerChanged`). The subject of a principal is
  `<provider ID>:<subject at the identity provider>`.
- `internal/oauth.Identity` has no groups. The sign-in reads no group claim.
  `principal.MembershipSource` (`local`, `scim`, `claim`) exists in the model
  only.
- `internal/stepup` holds the ports of RFC 102 and the case names of its
  suite. Every case skips. It has a TOTP port (`TOTPStore`), and no WebAuthn
  and no recovery-code type.
- `pkg/ipallow` holds the namespace lists only. `ipallow.Gate` decorates the
  `Authorizer`. The client address comes from `internal/clientip`, which walks
  `X-Forwarded-For` or `Forwarded` from the right past `--trusted-proxies`
  (ADR 212 decision 4). The flags `--ip-allowlists` and
  `--ip-allowlists-include-bootstrap` exist.
- The server TLS configuration has no `ClientAuth` and no client CA. The
  token exchange of ADR 54 does not exist yet. The credential kind
  `trust-token` exists.
- `accesstoken.ExpiryWarning` gives the warnings of ADR 69 decision 5 (14 days
  and 3 days) in the API, in the header `Dustpan-Token-Expires-At` and in the
  `opendustpan` CLI. No SMTP code exists. `pkg/notify` declares the sink kind
  `email` and the `Sender` port. It has no email sender.
- `host create-temp-admin` (ADR 245) accepts the recovery mode
  `signed-request` the same as `bootstrap`. No code signs or checks a
  recovery request.
- The settings resource of ADR 212 decision 7 does not exist yet. Settings are
  flags and environment variables in `internal/cli/settings.go`, and the
  session policy has its own route.
- `go.mod` has `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2` as
  direct requirements. `golang.org/x/crypto` and `github.com/go-jose/go-jose/v4`
  are indirect only. No WebAuthn, TOTP, LDAP, SAML or SCIM module is in the
  module graph.
- The e2e suite runs a mock OIDC provider in process
  (`internal/oauth/oauthtest`). It starts no other identity server.

### Questions that accepted ADRs already answer

These items of "Open questions" above have an answer in an accepted ADR. An
ADR that accepts this proposal closes them in this RFC:

- The defaults for the idle timeout and the maximum length of a console
  session: ADR 69 decision 4 (15 minutes idle, 12 hours in all, both
  settings).
- How MFA freshness works with an SSO identity provider: ADR 71 and ADR 227
  (one window, `freshAuthSeconds`, and a new authentication at the identity
  provider). P1 below adds the parameter of each new method.
- The format of the audit entry of a host recovery, with the name of a
  person and a reason: ADR 245 decision 3 for mode 1. P12 below gives the
  same for mode 2.

### P1. One sign-in path for every method

**Accepted, not reopened.** ADR 6: a sign-in method only proves who a
person is, and OpenDustpan issues its own session or token. ADR 55 decision 2:
each method is an adapter behind a port, with a conformance suite. ADR 70
decisions 12 to 22: one membership function applies the rules for groups from
an identity provider. ADR 216 decision 9: a sign-in finds or makes a principal
of its own home only. ADR 227: one fresh-authentication window.

**Proposal.** Three shapes of method share one result:

| Shape | Port (in `pkg/session`) | Methods |
| --- | --- | --- |
| Redirect | `RedirectMethod`: `Begin(ctx, BeginInput) (Redirect, error)`, `Finish(ctx, Callback) (Assertion, error)` | OIDC and OAuth 2.0 (exist), SAML, CAS |
| Password | `PasswordMethod`: `Verify(ctx, Credentials) (Assertion, error)` | LDAP |
| Request | `RequestMethod`: `FromRequest(ctx, RequestFacts) (Assertion, error)` | trusted header |

- `session.Assertion` holds the provider ID, the subject at the identity
  provider, the names and the email, the groups and whether the method sent
  groups at all (`GroupsPresent`), the authentication time and whether the
  method knows it (`AuthTimeKnown`), and the kind of the method. A method
  never makes a principal, a session or a membership.
- `RequestFacts` is a plain struct: the peer address, the client address from
  `internal/clientip`, the named headers only, and the verified TLS client
  chain. The domain package imports no `net/http` (depguard).
- After a method, the handler calls one function, in this order:
  `principal.SignInPrincipal` (exists), then a new
  `principal.ApplyGroupClaims` that holds ADR 70 decisions 13 to 22 and is
  the same function that SCIM calls (P8), then `session.Start` with the method
  kind and the authentication time.
- Passkey sign-in (P7) is not a method of this table. It is part of the local
  user sign-in, as ADR 55 decision 5 says, and it makes the same
  `Assertion`.
- `session.ProviderMode` adds `saml`, `cas`, `ldap` and `header`. Each mode
  has its own settings struct in `ProviderConfig`, as `OAuthSettings` does
  today. The provider admin routes of today serve each mode.
- The subject stays `<provider ID>:<subject at the identity provider>`. Each
  mode names the attribute that gives the subject (P2 to P6). ADR 70
  decision 22 says that the server never links by an attribute that can
  change. The server cannot prove that an attribute never changes, so the
  mode defaults to the attribute that the protocol defines as stable, and a
  change of the attribute is a sensitive action with an alert (ADR 70
  decision 22).
- Suites: `pkg/session/sessiontest.RunRedirectMethod`, `RunPasswordMethod`
  and `RunRequestMethod`. Each adapter runs its suite.

**Fresh authentication for each method.** ADR 227 decision 1 counts a new
authentication at the SSO identity provider. Each method asks for one with
the parameter of its protocol, and the server checks the time that comes
back:

| Method | How the server asks | What the server checks | Precedent |
| --- | --- | --- | --- |
| OIDC | `max_age=0` (exists) | `auth_time` in the ID token | OIDC Core: with `max_age`, the ID token "MUST include an auth_time Claim" (fetched) |
| SAML | `ForceAuthn="true"` | `AuthnInstant` of the `AuthnStatement` | SAML 2.0 core section 3.4.1 (not fetched) |
| CAS | `renew=true` on `/login` and on `/p3/serviceValidate` | validation succeeds only for a ticket from a new primary sign-in | Apereo CAS protocol 3.0 (not fetched) |
| LDAP | a new bind with the password | the time of the bind | the password check is the authentication |
| Plain OAuth 2.0 | no standard parameter | nothing | OAuth 2.0 has no authentication time |
| Trusted header | no parameter | nothing | the signed JWT of Google IAP has `iat`, not an authentication time (fetched) |

Plain OAuth 2.0 and trusted header cannot prove a recent authentication. So
ADR 227 gives no fresh authentication to their sessions, and each write that
needs one fails. Question O2 asks the owner to let a built-in factor (P7)
give the fresh authentication for these two methods. That amends ADR 46
decision 2 for these two methods only. RFC 102 asks a related amendment for
the CLI step-up. The owner can decide both together.

**Sign-in failures.** A password method and a passkey sign-in count failures
in `internal/authgate`. 10 failures in a row lock the user name for 30
minutes, or until an administrator unlocks it (PM decision). Precedent: PCI
DSS v4.0.1 requirement 8.3.4, as widely quoted: a lockout after not more than
10 attempts, for at least 30 minutes or until a person checks the identity (not
fetched). The answer is the same for an unknown user name and a wrong
password, so the answer does not tell that a user exists.

### P2. LDAP

**Accepted, not reopened.** ADR 55: LDAP is a sign-in method in the binary,
and a removal disables the sessions and tokens (revocation at once is
opt-in). ADR 70 decision 19: a termination applies at once. ADR 46: built-in
MFA applies to LDAP users.

**Open question: how does the server find that LDAP removed a user?**

| Option | Gives | Costs |
| --- | --- | --- |
| A. At the next sign-in only | No background work. | A removed user keeps a session and tokens until the next sign-in, which can be never. |
| B. A sync at an interval only | Finds removals of users who do not sign in. | A removed user can sign in until the next sync. |
| C. Both (lead proposal) | Each sign-in checks the directory, and the sync finds the rest. | One durable job. |

Precedent:

- GitLab checks each LDAP user in a daily worker, at 01:30 by default, and
  blocks a user that fails the check. A user is also blocked when the LDAP
  server is unreachable at sign-in. Group sync runs each hour (fetched,
  docs.gitlab.com, LDAP synchronization).
- Keycloak has a periodic full sync and a periodic sync of changed users. The
  sync of changed users does not find deletions. Only the full sync does
  (fetched for the two kinds. The note on deletions is from a search
  summary, not fetched).
- Vault checks LDAP at login only. A change of group membership does not
  change a token that exists (fetched).
- AWS, Google Cloud and TFE have no LDAP sync (not fetched).

**Recommendation: C (PM decision).** A clear precedent exists (GitLab), and
the option takes nothing from any persona. Details:

- Each sign-in binds as the user. A user that the search does not find, or
  that the directory reports as disabled (the attribute is a setting, for
  example `userAccountControl` bit 2 for Active Directory), is a termination
  (ADR 70 decision 19).
- The sync is a durable operation (`pkg/durable`) with the interval
  `ldap.syncIntervalSeconds`, 3600 by default, at least 300. It runs a full
  search, because a sync of changed entries does not find deletions. One hour
  follows the group sync of GitLab. A longer interval is a weaker setting
  (ADR 212 decision 3).
- An unreachable directory fails closed for a sign-in. For the sync, an
  unreachable directory changes no user and raises an alert. It never
  disables every user, because a network fault is not a termination.
- A sync that would disable more users than the limit of the mass-suspension
  alert (P8) still applies each termination, as ADR 70 decision 19 requires,
  and raises the alert.

**Design.**

- The adapter binds with a service account, searches for the user with a
  filter that the administrator sets, then binds as the user to check the
  password. It never reads or compares a password hash.
- Filter values pass through the LDAP filter escape of RFC 4515, and DN values
  through the DN escape of RFC 4514. The user name has an allowlist at the
  boundary: at most 256 characters, no control characters.
- The subject is `entryUUID` (RFC 4530) by default, `objectGUID` for Active
  Directory, or an attribute that the administrator names. Never the DN, the
  `uid` or the email, because they can change (ADR 70 decision 22).
- Groups: a group search or `memberOf`, by a setting. A nested group of the
  directory gives a flat membership, because groups do not nest (RFC 19,
  section of 2026-10-30). The groups go through
  `principal.ApplyGroupClaims` with the source `ldap`.
- TLS: `ldaps://` or StartTLS, with certificate checks and an optional CA
  bundle. Question O3 asks whether the server refuses a connection with no
  TLS, except to a loopback address.
- The service account password is a sensitive setting. It comes only from an
  environment variable or a `*_FILE` file, as the SMTP password (ADR 212
  decision 2).
- Timeouts: 10 seconds for a connection, 10 seconds for an operation, at most
  1000 entries for a search of groups. A size limit of the directory answer
  is a refusal, not a partial result.

### P3. SAML

**Accepted, not reopened.** ADR 55: SAML is a sign-in method in the binary.
ADR 46 decision 2: MFA is the job of the identity provider.

**Open questions.**

1. **Start of the flow.** A sign-in that the service provider starts (an
   "SP-started" sign-in) has an `AuthnRequest` with an ID, and the server
   checks `InResponseTo`. A sign-in that the identity provider starts (an
   "IdP-started" sign-in) has no request. So it has no binding to a browser session, and
   a stolen assertion can sign in another browser (login CSRF).
   - A. SP-started only. A tile in the portal of the identity provider
     points to the sign-in URL of OpenDustpan, which starts an SP-started
     flow.
   - B. Both, with IdP-started sign-in off by default.

   Precedent: GitLab and Keycloak accept IdP-started sign-in (not
   fetched). No precedent refuses it in general. **Recommendation: A
   (owner decision O4),** because B adds a path with no binding, and A keeps
   the tile through a bookmark URL.
2. **Fresh authentication.** `ForceAuthn="true"` and a check of
   `AuthnInstant`, as P1 says (PM decision, the same shape as ADR 71).
3. **Single logout.** Not in the milestone 4 list (ADR 55). A console session
   ends after 12 hours at most (ADR 69). This proposal adds no single logout.
   This is not a scope cut, because no plan names it.

**Design.**

- HTTP-Redirect binding for the request, HTTP-POST binding for the response.
- The server refuses an unsigned assertion. It accepts a signed response
  only when the response holds exactly one assertion. It refuses SHA-1 and
  every signature algorithm outside an allowlist (RSA-SHA256 and stronger,
  ECDSA-SHA256 and stronger).
- It checks `Destination`, `Recipient`, `Audience`, `InResponseTo`,
  `NotBefore` and `NotOnOrAfter`, with at most 120 seconds of clock skew.
- It stores the assertion ID until `NotOnOrAfter` and refuses a second use
  (replay).
- The body is at most 256 KiB before decode. The XML parser refuses a DTD,
  an entity and a depth of more than 64.
- Encrypted assertions are optional, with a key of the installation that the
  secret package seals.
- The subject is a persistent `NameID` by default, or an attribute that the
  administrator names. A transient or email `NameID` is refused as a subject,
  because it changes.
- Groups come from an attribute that the administrator names.
- The metadata of the identity provider comes from a URL (through the egress
  rules of `pkg/egress`) or from an upload. The server pins the signing
  certificates and shows a change of them as a sensitive change with an
  alert (ADR 70 decision 22).
- The server publishes its own SP metadata at a public route.

**Library.** XML signature checks are the part of SAML with the most bypass
bugs. The project should not write its own. The options:

| Option | License | Last release | Known issues (OSV, fetched) |
| --- | --- | --- | --- |
| `github.com/russellhaering/gosaml2` with `goxmldsig` and `beevik/etree` | Apache-2.0, Apache-2.0, BSD-2-Clause | v0.12.0 (2026-08-05), v1.6.1 (2026-08-04), v1.8.1 (2026-09-23) | gosaml2: CVE-2020-29509, CVE-2023-26483, GO-2026-4760, GO-2026-4764, GHSA-5684-g483-2249. goxmldsig: CVE-2020-15216, CVE-2020-7711, CVE-2026-33487, GHSA-rrfw-hg9m-j47h |
| `github.com/crewjam/saml` (uses `goxmldsig` and `etree` too) | BSD-2-Clause | v0.5.1 (2025-04-14) | CVE-2020-27846, CVE-2022-41912, CVE-2023-28119, CVE-2023-45683 |
| Project code | none | none | none known, and no review history |

**Recommendation: gosaml2 (owner decision O11, a new dependency).** It has
releases in 2026 with the security changes of 2026, it is a service provider only, and
crewjam/saml has had no release since April 2025. Its history of bypass bugs
is the reason for the layers below: the server checks the count of
assertions, the algorithm allowlist and the replay itself, as if the library
had failed.

### P4. CAS

**Accepted, not reopened.** ADR 55: CAS is a sign-in method in the binary.

**Design (PM decisions, no owner question).**

- CAS protocol 3.0. The server sends the browser to `<cas>/login?service=...`
  and checks the ticket with a server-to-server call to
  `<cas>/p3/serviceValidate`, over HTTPS only, through the egress rules.
- The `service` URL comes from the configured external URL of the
  installation and the provider ID. It never comes from the request.
- A ticket has an allowlist at the boundary: it starts with `ST-`, at most 256
  characters, and the characters `A-Za-z0-9-._`. The server uses a ticket
  once.
- The server never sends `pgtUrl`. It accepts no proxy ticket.
- The validation answer is at most 64 KiB of XML, with no DTD.
- The subject is an attribute that the administrator names. With no
  attribute, the subject is the CAS `user`, and the provider page says that a
  rename in the CAS server makes a new principal.
- Groups come from an attribute that the administrator names.
- Fresh authentication: `renew=true` (P1).

**Library.** No new module. The protocol is a redirect and one XML answer,
which `net/http` and `encoding/xml` handle. `gopkg.in/cas.v2` (MIT, v2.2.3,
2025-09-13) is an HTTP middleware for a CAS client, and it does not fit the
sign-in path of P1.

### P5. OAuth 2.0

**Accepted, not reopened.** ADR 55 decision 7: any OAuth 2.0 provider, with a
configured user-info endpoint and claim mapping.

**On `main`.** The sign-in exists: `session.ModeOAuth`, `oauth.PlainClient`,
the claims for the subject, the name, the user name and the email, PKCE, and
the issuer binding `userinfoURL#subjectClaim`.

**What is left (PM decisions).**

- A groups claim in `OAuthSettings` (`GroupsClaim`), a JSON array of strings
  in the user-info answer, at most 1000 entries of at most 256 characters
  each. The groups go through `principal.ApplyGroupClaims` with the source
  `claim`. A provider that needs more API calls for groups (for example the
  teams of a Git host) is not in scope: only the user-info answer counts.
- The user-info URL goes through the egress rules of `pkg/egress`, so a
  provider setting cannot reach an address that the egress rules refuse.
- Fresh authentication: none (P1, question O2).

### P6. Trusted-header sign-in

**Accepted, not reopened.** ADR 55 decision 6: off by default, the operator
names the proxies, and the server ignores the header from any other source.
ADR 212 decision 3: trusted-header sign-in from off to on is a weaker
setting. ADR 250: no layer alone.

**Open question 1: a plain header, a signed header, or both?**

Precedent:

- Grafana reads `X-WEBAUTH-USER` and trusts it from an allowlist of proxy
  addresses (fetched).
- Google IAP sends the JWT `x-goog-iap-jwt-assertion`, signed with ES256.
  Google says that the unsigned headers can be forged when a request goes
  around IAP, and that a service must check the JWT (fetched).
- An AWS Application Load Balancer sends the JWT `x-amzn-oidc-data`, signed
  with ES256, and a service must check the `signer` field (fetched).
- Kubernetes trusts the `X-Remote-User` header of a front proxy only after a
  TLS client certificate of the proxy (not fetched).

**Recommendation (PM decision): two modes, and each mode has two layers.**

- `jwt` mode: the server checks the signature of the JWT against the keys of
  the issuer (a JWKS URL or the key URL pattern of the load balancer), and
  the issuer, the audience, `exp` and `iat` with at most 60 seconds of skew.
  The peer must also be in `--trusted-proxies`. Presets for Google IAP and
  AWS ALB fill the header name, the key URL and the issuer.
- `plain` mode: the peer must be in `--trusted-proxies`, and the request
  must also carry a second proof of the proxy: a TLS client certificate of
  the proxy that the trust bundle of P10 checks, or a shared secret header
  that the server compares in constant time. A plain header from a trusted
  address alone is one layer, which ADR 250 refuses.
- In both modes, the server removes the user and groups headers of a request
  from any other peer before a handler reads it, and writes an audit entry
  for a refused header.

**Open question 2: groups from a header?** Lead proposal: yes, from a second
header that the operator names, off by default. Precedent: Grafana reads
groups from a header that the operator names, and an empty or missing groups
header removes the user from all teams (fetched). **Recommendation: yes (PM
decision)**, with the rules of ADR 70: the groups go through
`principal.ApplyGroupClaims` with the source `claim`. An empty header is an
empty list. A missing header with groups on is a refusal of the sign-in,
because a proxy that dropped the header must not remove every membership.
In `jwt` mode, groups come from a claim that the operator names.

**Subject.** The `sub` claim in `jwt` mode. The user header in `plain` mode,
with the allowlist of characters of a subject at the boundary.

**Fresh authentication.** None (P1, question O2).

### P7. Passkeys and built-in MFA

**Accepted, not reopened.** ADR 46: built-in MFA for installs with no SSO
identity provider, WebAuthn and TOTP of the same rank, recovery codes shown
one time, off by default, admin reset with an audit entry, a separate setting
for a secret reveal, access tokens stay bearer tokens. ADR 55 decision 5: a
local user can sign in with a passkey alone. ADR 70 decision 23: each
break-glass account signs in with a passkey or a hardware key. ADR 227: one
fresh window. RFC 102 (discussing): one TOTP factor for each person, shared by
built-in MFA and the CLI step-up, with RFC 6238 parameters, sealed storage,
one use of each time step and separate failure counters.

**A fact that sets the order.** ADR 70 decision 24 says that the first setup
cannot finish without two break-glass accounts with passkeys or hardware
keys, unless a self-hosted operator turns break-glass off. No WebAuthn code
exists. So WebAuthn is the first work item of this proposal (W3).

**Open question 1: does a local user also have a password?** ADR 46 names "a
local user name and password" as an example of an install with no identity
provider. ADR 55 lists "passkeys for local users" and no local password. No
code stores a password today.

| Option | Gives | Costs |
| --- | --- | --- |
| A. A passkey is the only sign-in of a local user | No password store, no password reset, no hash to steal. A passkey is phishing resistant. | A person with no device that can hold a passkey cannot be a local user. The person can sign in through a method of P2 to P6. |
| B. A password, and a passkey as an option | Works on any device. | A password store, a reset flow, a hash function and a rule set for passwords (PCI DSS 8.3.6 asks for 12 characters, not fetched). |

Precedent: GitLab and Keycloak have local passwords (not fetched). AWS
supports passkeys as an MFA factor for root and IAM users (fetched). No
precedent has a passkey as the only local sign-in. **Recommendation: A
(owner decision O1).** The milestone 4 list names passkeys, not passwords, and
A keeps a password store out of the product. B is a later RFC if a persona
needs it.

**Open question 2: does "require MFA" apply to the bootstrap admin token?**
Lead proposal: no.

Precedent: the Vault page on tokens says nothing of MFA for a root token, and
tells the operator to revoke it as soon as possible (fetched). AWS requires
MFA for the root user of each account, within 14 days of the first console
sign-in (fetched). The AWS root user signs in to a console. The bootstrap
token is a bearer token like an access token, and ADR 46 decision 8 keeps
access tokens without an MFA step.

**Recommendation: no (owner decision O12),** because the precedents point in
two directions. The controls that stay: the server reads the token from the
environment, the console and the API ask the operator to retire it (ADR 53
decision 9), each use writes an audit entry, and the IP allowlist can include
it (`--ip-allowlists-include-bootstrap`).

**WebAuthn design (PM decisions, with precedent).**

- The relying party ID is the host of the configured external URL. The server
  checks the origin of each ceremony against that URL. A change of the host
  makes each passkey unusable. The operator guide says so, and the doctor
  check of ADR 238 warns when the external URL changes.
- A passkey sign-in needs user verification (`required`) and a discoverable
  credential. A second factor after another method asks for user
  verification as `preferred`. Precedent: the Keycloak policy for passwordless
  WebAuthn requires user verification and a resident key (search summary, not
  fetched).
- Attestation is `none` by default. An optional setting holds an allowlist of
  authenticator models (AAGUIDs) and then requires attestation. The
  installation can set it for break-glass accounts only, for all persons, or
  not at all. A synced passkey has no attestation, so the allowlist excludes
  synced passkeys. NIST SP 800-63B-4 allows a syncable authenticator at AAL2
  and not at AAL3 (fetched).
- The signature counter: a counter that goes back suspends the credential and
  writes a `security` audit entry, except a counter that is 0 on each use,
  which synced passkeys send.
- At most 20 credentials for each person. Each credential has a name that the
  person types, with the character rules of the name of a principal.
- A ceremony has a challenge of 32 random bytes, stored for 300 seconds, used
  once.

**TOTP (PM decision).** Follow RFC 102 question 5, option B: one factor, on
the standard library (`crypto/hmac`), HMAC-SHA-1, 6 digits, 30 seconds, the
current step and one step before, sealed with the secret package, one use of
each step (NIST SP 800-63B-4: "SHALL accept a given OTP only once while it is
valid", fetched). The console sign-in has its own failure counter (P1).

**Recovery codes (PM decision).** 10 codes, each of 16 characters of base32
(80 bits). The server stores the SHA-256 of each code, shows the codes one
time, and uses each code once. A new set replaces the old set. A sign-in with
a recovery code writes an audit entry, gives no fresh authentication for a
factor add (RFC 102 question 5), and the console then asks the person to
enroll a new factor. Precedent: GitHub gives 16 codes (fetched). GitLab and
Google give 10 (not fetched).

**Enforcement (PM decision).** When "require MFA for all users" is on, a person
with no factor receives, at the next sign-in, a session that can only enroll a
factor and sign out. The setting `mfa.enrollmentGraceDays`, 0 by default and
at most 14, lets a person skip the enrollment for that many days after the
setting changes. Precedent: AWS gives 14 days to register MFA for a root user
(fetched). A longer grace is a weaker setting (ADR 212 decision 3).

**Sign-in flow.** A first method (a password method, or a redirect method
with built-in MFA in an installation with no SSO provider) makes a pending
sign-in, not a session. A pending sign-in lives 300 seconds, holds no rights,
and takes 5 wrong factors before it is void. The factor then turns it into a
session that records the method, the factor kind and the time of the factor
(the fresh time of ADR 227).

**Admin reset (ADR 46 decision 9).** The route needs
`installation:manage-sign-in` (ADR 70 decision 11, with the name of ADR 210), a fresh
authentication, a reason, and a reach that covers the reach of the person
(ADR 70 decision 22: a credential change for a member of a protected group is
a protected action). The reset revokes every factor, every recovery code and
every step-up credential of the person, in one transaction with the audit
entry. A reset of the own account is refused.

**Settings** (installation, in the settings resource of ADR 212 decision 7):
`mfa.enabled`, `mfa.requireForAll`, `mfa.requireForSecretReveal`,
`mfa.enrollmentGraceDays`, `mfa.webauthn.allowedAAGUIDs` and
`mfa.webauthn.allowedAAGUIDsScope`. Each "on to off" change is a weaker
setting (ADR 212 decision 3 lists the first three). A static lock of
`mfa.requireForAll` is a security switch of ADR 219 decision 4.

**Library.** WebAuthn needs CBOR, COSE keys and attestation formats. The
project should not write its own. `github.com/go-webauthn/webauthn`
v0.18.2 (2026-09-19), BSD-3-Clause, no OSV entry (fetched). Its transitive
modules (for example a CBOR module and a JWT module) are part of the request
to the owner (O9). The work item lists them with `go mod graph` and runs
`make licenses` and `make trivy` before the owner answers. `github.com/pquerna/otp`
(Apache-2.0) is not needed, because TOTP is short on the standard library.

### P8. SCIM 2.0

**Accepted, not reopened.** ADR 55: SCIM 2.0 makes, changes and removes users
and groups, and its groups are the groups of ADR 52. ADR 70 decisions 12 to
22: membership source, protected groups, a termination at once, suspended
memberships, mass-suspension alerts, an alert before a SCIM token expires.
RFC 19 lead decision: a group whose source is not `local` refuses a
membership change through the API with 409.

**Open question 1: a SCIM module or the standard library?**
`github.com/elimity-com/scim` (MIT) has no tagged release, only a
pseudo-version of 2026-07-28 (fetched). **Recommendation: the standard
library (PM decision under the rule of CLAUDE.md: prefer the standard
library).** The part that clients use is small (see the list below).

**Open question 2: what does the server serve?** Precedent:

- TFE has no bulk, and filters with `eq` only: users on `userName` and
  `externalId`, groups on `displayName` and `externalId` (fetched).
- AWS IAM Identity Center has no `/Bulk`, `/Me` or `/Search`, `eq` with `and`
  only, single-value attributes only, and no `password` (fetched).
- GitLab self-managed tests with Okta and Microsoft Entra ID only, and a
  deprovision blocks the user (`active: false`) (fetched).

**Recommendation (PM decision): the subset that TFE and AWS serve.**

- `/ServiceProviderConfig`, `/ResourceTypes` and `/Schemas`.
- `/Users`: create, read, list, replace (`PUT`), `PATCH` and `DELETE`. The
  attributes: `userName`, `externalId`, `active`, `name`, `displayName` and
  one `emails` value. Unknown attributes are refused with the SCIM error
  `invalidValue`, not dropped.
- `/Groups`: create, read, list, replace, `PATCH` (`add`, `remove` and
  `replace` of `members`, including `remove` with no filter, which RFC 7644
  section 3.5.2 allows) and `DELETE`. The attributes: `displayName`,
  `externalId` and `members`.
- Filters: `eq` on `userName`, `externalId` and `displayName`, joined with
  `and`. Any other filter answers `invalidFilter`.
- No bulk, no sort, no `/Me`, no `/Search`, no `password`. The
  `ServiceProviderConfig` says so.
- The forms that Microsoft Entra and Okta send: operation names in any case,
  `PATCH` of `active` as a string `"False"`, and `PUT` of a whole user
  (not fetched). The e2e fixtures hold the recorded shapes.

**Connections and tokens (PM decision).** A SCIM connection belongs to the
installation, or to a namespace when `identity.principalHome` is `namespace`
(ADR 215 decision 10). Its base URL is `/scim/v2/{connectionID}`. A SCIM token
is a credential of the connection, stored as a hash, with an expiry of at most
365 days (ADR 69), and at most two live tokens for a rotation. Precedent: AWS
IAM Identity Center gives a token of 1 year and reminds from 90 days before
the expiry (fetched). HCP Terraform allows SCIM tokens of 30 days to 1 year
(search summary, not fetched). The alert of ADR 70 decision 22 uses the
warning days of ADR 69 decision 5 (14 and 3 days).

**Pairing with a sign-in method (PM decision).** A SCIM user must be the same
principal as the person who signs in. Each connection names one sign-in
provider and a matching rule: SCIM `externalId` equals the subject of that
provider (the `NameID` of SAML, or a claim of OIDC that the administrator
names). The server never matches on `userName` or an email (ADR 70 decision
22). A change of the matching rule is a sensitive action with an alert.
Precedent: GitLab requires SAML first and keeps the SAML identity link on a
deprovision (fetched). TFE requires SAML first (fetched).

**Removals.** `active: false` and `DELETE` are terminations (ADR 70 decision
19). `DELETE` keeps the row for the history (RFC 19 lead decision), and a
later `GET` answers 404.

**Mass-suspension alert (ADR 70 decision 21, the limit is open).**
Recommendation (PM decision): alert when one connection removes more than 25
memberships, or more than 10 percent of the members of the protected groups,
in one hour. Both are settings. The alert never stops a removal. Precedent:
Microsoft Entra provisioning has an "accidental deletions" threshold that an
administrator sets (not fetched).

**Limits.** A body of at most 1 MiB, at most 1000 members in one `PATCH`, at
most 100 results in one page. A SCIM token has its own rate limit. The SCIM
routes are on the route test with the action `scim:write` on the connection,
which only a SCIM token holds.

### P9. IP allowlists for the installation and for tokens

**Accepted, not reopened.** ADR 55 decision 8: off by default, an
administrator turns the feature on, and then the installation, each namespace
and each token opt in. Each list applies to console sign-in and to requests
with an access token. ADR 212 decision 4: an unknown client address is
refused. ADR 219 decision 4: the feature switch is a security switch. ADR 218:
the namespace list has the namespace as its object.

**On `main`.** The namespace lists, the gate on the `Authorizer`, the client
address, `--ip-allowlists` and `--ip-allowlists-include-bootstrap`.

**Open question 1: how do the lists combine?** Lead proposal: a request must
pass each list that applies to it.

Precedent:

- AWS: an explicit deny in any policy layer overrides each allow, so an
  `aws:SourceIp` deny in one layer blocks the request (fetched).
- Vault binds a token to CIDRs, and the CIDRs of the role are a separate check
  at login (fetched for the token binding. Not fetched for the two layers).
- HCP Terraform: with lists for an agent pool, a request must be inside the
  ranges of the organization and of the pool (search summary, not fetched).
- GitHub Enterprise Cloud: an organization inherits the entries of the
  enterprise, which reads as one joined list (fetched). This is the one
  precedent for a union.

**Recommendation: each applicable list must pass (PM decision).** Most
precedents give an intersection, and a union would let a namespace widen a
list of the installation, which no persona asks for.

**Open question 2: which requests?** ADR 55 says console sign-in and requests
with an access token. Other credentials of a principal exist: a console
session after sign-in, an exchanged workload token (ADR 54), a SCIM token (P8),
a job token and a runner token (ADR 133).

| Option | Gives | Costs |
| --- | --- | --- |
| A. Sign-in and access tokens only, as written | No change to runners, SCIM or workloads. | A stolen session cookie or a SCIM token works from any address. |
| B. Every credential of a principal | One rule. A stolen credential of any kind fails outside the lists. | A runner, a SCIM client of a cloud identity provider and a workload must be inside the lists of their scope. |

Precedent: GitHub applies its list to personal tokens, OAuth and GitHub App
tokens and SSH keys (fetched). GitLab applies its group list to the UI, the
API, group and project tokens, service accounts and deploy tokens (fetched).
HCP Terraform has separate lists for agent pools (fetched). **Recommendation:
B (owner decision O5).** Inbound webhooks of a forge carry no credential of a
principal and keep their signature checks (ADR 271). The guide lists the
address ranges that a SCIM client of a cloud identity provider uses.

**Open question 3: break-glass accounts.** ADR 70 decision 24 counts only an
account that a policy does not block. An installation list can block a
break-glass person who is outside the list in a lockout.

| Option | Gives | Costs |
| --- | --- | --- |
| A. The installation list applies to break-glass accounts | A stolen passkey session fails outside the list. | A break-glass person outside the list cannot sign in. The host recovery of ADR 245 still works, with no public listener. |
| B. Break-glass accounts skip every list | The accounts always work. | An account of last resort is the account with the most rights and no network limit. |

Precedent: GitLab lets administrators reach each project through a group
restriction (fetched). HCP Terraform lets organization owners reach the
platform always (fetched). Both exempt the top role. **Recommendation: A
(owner decision O6),** because the host recovery path is the fallback for a
lockout and the precedents exempt a role, not an emergency account. The guard
of ADR 70 decision 24 then does not count an IP list as a block, and the guide
tells the operator to keep the break-glass locations inside the list.

**Design (PM decisions).**

- The installation list is the settings key `ipAllowlist.installation` in
  the settings resource, with `ipAllowlist.enabled` as the feature switch.
- A token list is a field of the token, `allowedCidrs`, set when the token is
  made and copied by a rotation. A change makes a new token. Precedent: a
  Vault token binds its CIDRs when it is made (fetched).
- At most 256 entries in each list. An entry is a CIDR with no host bits. The
  server refuses `/0`, as for `--trusted-proxies`.
- The namespace list of a request is the namespace of the object of its route
  (ADR 218). A route on `@server` checks the installation list and the token
  list.
- A change of the installation list that leaves the client address of the
  caller outside the list fails with `ipallow.self-lockout`, unless the
  request holds `confirmLockout: true` and a fresh authentication.
- A refused request answers 403 with the error code `ip.not-allowed` and
  names the list (installation, namespace or token), not the entries. It
  writes an audit entry with the client address.

### P10. mTLS

This subsection answers the mTLS questions of [RFC 20](0020-access-tokens.md)
too.

**Accepted, not reopened.** ADR 54: mTLS client certificates are a kind of
workload identity in milestone 4. The server checks a certificate against a
trust bundle that an administrator sets. The identity is a SPIFFE ID in the
URI SAN or another field that a trust rule names. A trust rule belongs to one
namespace and names a principal that is not a person. ADR 69 decision 6: an
exchanged token lives 1 hour.

**On `main`.** No token exchange and no trust rule code exist. The token
exchange of ADR 54 is a prerequisite of mTLS, so it is work item W20.

**Open question 1: can a workload use mTLS at the exchange endpoint, to receive a
token for a tool that cannot send a certificate?** Lead proposal: yes. The
Pulumi CLI and OpenTofu send a bearer token to the tool-compatible APIs, and
the e2e suite must show which can send a certificate (RFC 20). **Recommendation:
yes (PM decision).** The exchange endpoint takes a client certificate as well
as a JWT, and answers the same token. A direct mTLS request to an API, with no
token, is also accepted when a trust rule matches.

**Open question 2: a proxy that ends TLS.** Lead proposal: the proxy sends the
checked certificate in a header, accepted only from named proxies.

Precedent:

- Vault reads the certificate from a header that the listener names, with
  decoders for Base64, DER and URL forms, and only from the addresses in
  `x_forwarded_for_authorized_addrs`. Its cert auth page says that the
  security depends on the load balancer and recommends mTLS between the load
  balancer and Vault (fetched).
- An AWS Application Load Balancer in passthrough mode sends
  `X-Amzn-Mtls-Clientcert` with no check, and the target must check it
  (fetched).
- Envoy sends `x-forwarded-client-cert` and removes it from clients by
  default (fetched). The Google Cloud load balancer has header variables
  with RFC 9440 encoding (fetched).

**Recommendation (PM decision).** The setting `--client-cert-header` names one
header and its encoding (`rfc9440`, `pem-url` or `xfcc`). The server reads it
only from a peer in `--trusted-proxies` that also proves itself as P6 says
(a TLS client certificate of the proxy, or the shared secret header). The
server then checks the chain of the certificate against the trust bundle
itself, as if the proxy had checked nothing. This is the passthrough model of
AWS, and it does not depend on the proxy.

**Design (PM decisions).**

- Direct mode: the listener sets `tls.VerifyClientCertIfGiven`, so a request
  without a certificate still works with a token. The server checks the chain
  with `crypto/x509` against the trust bundles of the matching rules, never
  against the system roots.
- A trust bundle belongs to a namespace (ADR 54 decision 5) and holds at most
  50 CA certificates. It can hold CRLs that the administrator uploads. The
  server downloads no CRL and no OCSP answer, because the bundle owner controls
  the lifetime of short-lived certificates (SPIFFE) and an outbound download
  adds an egress path.
- The server refuses a certificate with a key below RSA 2048 or P-256, a
  certificate that lives more than 397 days, and a chain of more than 5
  certificates.
- RFC 8705 binds a token to a certificate (`cnf` with `x5t#S256`, fetched).
  Not in milestone 4: the tool CLIs cannot send a certificate, so a bound
  token would not work for them. The exchanged token lives 1 hour (ADR 69).
- No new module: `crypto/tls` and `crypto/x509` of the standard library.

### P11. The token expiry email

This subsection answers the email questions of
[RFC 20](0020-access-tokens.md) too.

**Accepted, not reopened.** ADR 53 decision 11: an email to the owner of the
token. ADR 69 decision 5: 14 days and 3 days before the expiry, both settings.
ADR 212 decision 2: SMTP settings are installation settings, and the password
comes only from an environment variable or a `*_FILE` file. ADR 145: email is
a sink kind, and the sender uses the standard library. ADR 234: emails are
localized.

**Open question 1: the names of the SMTP settings (ADR 212, not decided).**
Recommendation (PM decision): `smtp.host`, `smtp.port`, `smtp.from`,
`smtp.username`, `smtp.password` (environment or `*_FILE` only), `smtp.tls`,
`smtp.caFile` and `smtp.timeoutSeconds` (default 30).

**Open question 2: does email need TLS?** Precedent: GitLab has STARTTLS on
port 587 and implicit TLS on port 465, which exclude each other, and a
setting for the certificate check (fetched).

| Option | Gives | Costs |
| --- | --- | --- |
| A. TLS always: `starttls` (default) or `implicit`, with a certificate check | No credential or token name in clear on the network. | A relay with no TLS needs a local relay. |
| B. Option A, plus `none` for a relay on a loopback address | A local MTA (for example Postfix on the host) works. | One more value. |
| C. `none` for any relay | Works with any relay. | The SMTP password and the email cross the network in clear. |

**Recommendation: B (owner decision O3, with the LDAP question).** The server
refuses `smtp.tls: none` for any address that is not a loopback address. The
`net/smtp` client refuses the PLAIN authentication without TLS, except to
localhost (not fetched).

**Open question 3: who receives the email of a service token?** A service
principal has no email. Recommendation (PM decision): the persons who hold
`principal:manage-tokens` on the principal (ADR 67), at most 50, and the maker
of the token when the maker is a person. Precedent: GitLab sends the expiry
email of a project access token to the project maintainers and owners (not
fetched).

**Design (PM decisions).**

- A durable operation runs each hour and writes a notification
  `token.expiry-warning` (ADR 145) for each token that crosses a warning day.
  The dedupe key is the token ID and the warning day, so a restart sends no
  second email.
- The email sender implements `notify.Sender` with `net/smtp` of the standard
  library. No new module.
- The recipient is the email of the person from the identity provider, from
  SCIM or from an administrator. The server parses each address with
  `net/mail` and refuses CR and LF in each header value.
- The email holds the token name, the expiry date and the console page of the
  token. It never holds a token secret or a link that acts without a sign-in.
- The precedent for the warning days: GitLab sends at 60, 30 and 7 days
  (fetched). HCP Terraform at 30 and 7 days (search summary, not fetched).
  ADR 69 already decided 14 and 3 days. This proposal does not change them.
- A person can turn off the email for its own tokens in its preferences
  (`pkg/preferences`). The API field and the console banner stay.

### P12. Break-glass mode 2: a signed recovery request

**Accepted, not reopened.** ADR 57: mode 2 is a recovery request signed with an
offline operator key, and the operator chooses the mode at first boot. ADR 70
decision 27: a recovery makes a temporary administrator for one named person,
with no shared account. ADR 245: `--recovery-mode signed-request`, the
temporary administrator of `host create-temp-admin`, a reason, a lifetime of
24 hours, the end of all access at the expiry, and a review after use.

**What mode 2 adds.** Mode 1 needs a shell on the host. Mode 2 recovers over
the network with a request that only the holder of the offline key can sign.
It needs no restart and no change of the configuration.

**Open question 1: the format of the key and of the request.** Lead proposal:
an Ed25519 key pair, and a small signed document with the installation, a
nonce and an expiry.

| Option | Gives | Costs |
| --- | --- | --- |
| A. SSH signatures (`ssh-keygen -Y sign`, the `sshsig` format), Ed25519 and `sk-ssh-ed25519` keys | Operators have `ssh-keygen`. The key can live on a FIDO2 hardware key. About 50 lines of wire parsing on `crypto/ed25519` and `crypto/sha512` (fetched: the format, and that `golang.org/x/crypto` v0.58.0 has no `sshsig` code). The same verifier serves the SSH factor of RFC 102. | The project writes the parser, with fuzz tests. |
| B. Raw Ed25519 with a format of the project (as signify) | The standard library only. | A new tool to make keys and to sign. No hardware key. |
| C. minisign | A known tool. | Needs `golang.org/x/crypto/blake2b`. No hardware key. |
| D. OpenPGP | A module that the registry already links. | A complex format for a small document. |

Precedent: Vault `operator generate-root` returns the new root token XORed
with a one-time pad or encrypted to a PGP key (fetched). No precedent signs a
recovery request.

**Recommendation: A (owner decision O7).** It needs no new module, it lets the
key live on a hardware key, and it shares the verifier with RFC 102.

**Design (PM decisions).**

- The operator registers the public key at first boot:
  `--recovery-public-key` or `DUSTPAN_RECOVERY_PUBLIC_KEY_FILE`. The server
  stores it with the mode. Only `ssh-ed25519` and `sk-ssh-ed25519@openssh.com`
  keys pass the allowlist.
- `opendustpan recovery request` writes a request document. One field on
  each line, with no CR in a value:

  ```text
  opendustpan-recovery-request v1
  installation <installation ID>
  person <name of the person>
  subject <subject of the new temporary administrator>
  reason <reason>
  nonce <32 random bytes, base64url>
  not-after <RFC 3339 time, at most 1 hour after now>
  proof-sha256 <SHA-256 of a 32-byte secret that only the CLI keeps>
  ```

- The operator signs it offline:
  `ssh-keygen -Y sign -f <key> -n opendustpan-recovery@<installation ID>`.
  The namespace keeps a recovery signature apart from each other signature of
  the same key.
- `opendustpan recovery submit` sends the document, the signature and the
  secret to the public route `POST /api/dustpan/v1/recovery/signed-requests`.
  The server checks, in this order: the mode is `signed-request`, the body is
  at most 8 KiB, each line matches its allowlist, the installation ID is its
  own, `not-after` is in the future and at most 1 hour away, the namespace of
  the signature, the signature against the stored key, the SHA-256 of the
  secret, and that the nonce is new. It stores the nonce until `not-after`.
- The secret makes a copied document useless: a person who reads the signed
  document in a ticket or a chat cannot use it without the secret.
- The route then calls the service of `host create-temp-admin` (ADR 245): one
  temporary administrator for the named person, with the reason, 24 hours,
  the break-glass controls and the review after use. The answer holds the
  token one time.
- The route has a rate limit of 5 requests each 15 minutes for each client
  address, applies the installation IP list (P9), and writes an audit entry
  for each refusal, with the reason code and no document text.

**Open question 2: can an operator change the mode after the first boot?**
Lead proposal: yes, through the API by an administrator, with an audit entry.
ADR 245 says that a stored choice wins over the setting.

| Option | Gives | Costs |
| --- | --- | --- |
| A. Never | The first choice holds. | A key that leaks or is lost forces a new installation, or a restore. |
| B. Through the API: a site administrator, a fresh authentication, a reason, an audit entry and an alert | A key rotation and a mode change are possible. | An attacker with a site administrator session can change the recovery path. |
| C. Through the host only (`host recovery-mode set`), as ADR 245 makes the host command the path for recovery | Needs a shell on the host. | Mode 2 exists for an operator with no shell. |

Precedent: Vault rekeys its unseal and recovery keys only with a quorum of the
old keys (not fetched). Keycloak makes a new bootstrap administrator from the
host at any time (fetched). **Recommendation: B (owner decision O8),** with
these limits: a change to mode 3 or a new key is a weaker setting (ADR 212
decision 3) and needs the fresh step, the change waits 24 hours before it
applies and sends the alert to each site administrator and each break-glass
account at once, and a site administrator can cancel it in that time.

**Mode 1, "works once" (from "Open questions").** Accepted ADRs answer it.
ADR 70 decision 32 changes ADR 57 decision 4: the recovery token makes a
temporary administrator (ADR 70 decision 27). ADR 245 gives that
administrator 24 hours and ends all its access at the expiry. This proposal
adds nothing to mode 1.

### New dependencies

CLAUDE.md asks the owner before each new dependency. Each work item that adds
a module lists the full transitive set with `go mod graph`, and runs
`make licenses`, `make trivy` and `make vuln` before the owner answers.

| Item | Module | License | Last release (fetched) | Question |
| --- | --- | --- | --- | --- |
| Passkeys, MFA, break-glass accounts | `github.com/go-webauthn/webauthn` and its transitive modules | BSD-3-Clause | v0.18.2, 2026-09-19 | O9 |
| LDAP | `github.com/go-ldap/ldap/v3` and its transitive modules (an ASN.1 BER module and an NTLM module, both MIT, not fetched) | MIT | v3.4.15, 2026-10-07 | O10 |
| SAML | `github.com/russellhaering/gosaml2`, `github.com/russellhaering/goxmldsig`, `github.com/beevik/etree` | Apache-2.0, Apache-2.0, BSD-2-Clause | v0.12.0, v1.6.1, v1.8.1 (2026-08 to 2026-09) | O11 |

No new module for CAS, OAuth 2.0, trusted header, TOTP, recovery codes, SCIM,
IP allowlists, mTLS, SMTP or mode 2. Each of these uses the standard library.
The `sshsig` verifier of P12 is project code on `crypto/ed25519` and
`crypto/sha256` (for `sk-ssh-ed25519`) and `crypto/sha512`.

### Security layers (ADR 250)

Each row is one layer of one item. Each layer has its own test that sends
hostile input to that layer alone, as if the other layers had failed.

| Item | 1. Boundary | 2. Encode or contain | 3. Limit | 4. Authorization at use | 5. Fail closed |
| --- | --- | --- | --- | --- | --- |
| LDAP | user name allowlist, at most 256 characters | RFC 4515 filter escape, RFC 4514 DN escape | timeouts, search size limit, lockout | the provider is of the home of the principal (ADR 216) | unreachable directory refuses the sign-in. Unknown answer refuses |
| SAML | body size, no DTD, depth 64, algorithm allowlist | one assertion, signature over the element that the server reads | 120 s skew, replay store | `InResponseTo` binds to the browser of the request | any check that fails refuses, with an audit entry |
| CAS | ticket allowlist | `service` URL from the server, egress rules | 64 KiB answer, one use | provider of the home | unknown XML refuses |
| OAuth 2.0 groups | claim type and size | egress rules on the user-info URL | 1000 groups | `ApplyGroupClaims` rules of ADR 70 | a malformed claim refuses the sign-in |
| Trusted header | trusted peer, header name allowlist | headers from other peers removed | 60 s skew in `jwt` mode | the second proof of the proxy | missing groups header refuses |
| WebAuthn | ceremony JSON with a strict decoder | origin and RP ID from the server URL | 300 s challenge, one use, 20 credentials | the credential belongs to the person of the ceremony | counter regression suspends the credential |
| TOTP and recovery codes | 6 digits. 16 base32 characters | sealed secret. Stored hash | one use of a step. 10 failures lock | the factor belongs to the pending sign-in | store error refuses |
| SCIM | strict decoder, attribute allowlist, filter grammar | placeholders in SQL, no filter text in SQL | 1 MiB, 1000 members, rate limit | `scim:write` on the connection. Protected groups (ADR 70) | unknown attribute or filter refuses |
| IP allowlists | CIDR parse, no host bits, no `/0` | addresses from `internal/clientip` only | 256 entries | each applicable list on each request | unknown address refuses |
| mTLS | header name and encoding allowlist | chain check against the bundle, never system roots | chain of 5, key sizes, 397 days | trust rule of the namespace names the principal | a parse error refuses |
| SMTP email | address parse with `net/mail` | CR and LF removed from each header | 30 s timeout, at most 50 recipients for a token | the recipient holds the token or the act-as right | TLS failure refuses the send, and the delivery history records it |
| Mode 2 | line allowlist, 8 KiB | signature namespace, server-made temporary administrator | 1 hour, rate limit, one nonce | the mode is `signed-request` and the key is the stored key | each failure refuses, with an audit entry |

Each check that repeats another layer has the comment of CLAUDE.md that
names the layers that it does not rely on.

### e2e steps

ADR 55 says that each method has an e2e step with a real or a test server
before it ships. The suite today runs one mock OIDC provider in process.

**Recommendation (PM decision):** two tiers.

- `make e2e` (each pull request): in-process test servers in Go, as
  `internal/oauth/oauthtest` is today: an LDAP test server, a SAML identity
  provider that signs with the test key, a CAS server, a SCIM client that
  replays the recorded request shapes of Okta and Microsoft Entra, an SMTP
  server that records messages, a proxy that sets the headers of P6 and P10,
  and a software WebAuthn authenticator. The test servers live in
  `internal/<method>test` packages and use only the standard library and the
  module of the adapter.
- `make e2e-identity` (nightly, not part of `make check`): real servers in
  containers, pinned by digest as in `deploy/compose`: OpenLDAP, Keycloak for
  SAML, Apereo CAS, oauth2-proxy for the trusted header, and Mailpit for SMTP.
  This tier shows that the in-process servers match the real ones.

The steps that prove each item:

| Item | e2e step |
| --- | --- |
| LDAP | a person signs in with an LDAP password. `opendustpan` makes a token. The directory removes the person. The sync disables the session and the token. The Pulumi CLI and OpenTofu receive 401 |
| SAML | SP-started sign-in. A replayed assertion fails. An IdP-started response fails. A fresh step uses `ForceAuthn` |
| CAS | sign-in. A reused ticket fails. A fresh step uses `renew=true` |
| OAuth 2.0 groups | a sign-in adds the groups of the claim. A protected group refuses the add (ADR 70) |
| Trusted header | `jwt` mode sign-in. A forged header from an untrusted peer fails. A plain header without the second proof fails |
| Passkeys and MFA | a local user signs in with a passkey. MFA on and required. TOTP sign-in. A replayed code fails. A recovery code works once. An admin reset |
| SCIM | `POST`, `PUT`, `PATCH` with `active: false`, and `DELETE` of a user. Group `PATCH`. The person signs in through the paired provider. The deactivate stops the tokens of both tool CLIs |
| IP allowlists | an installation list, a namespace list and a token list. A request outside one of them fails for the Pulumi CLI and OpenTofu |
| mTLS | a workload sends a client certificate to the exchange endpoint and receives a token. It runs `tofu plan` with the token. A certificate of another bundle fails. The forwarded-header mode behind the test proxy |
| Expiry email | a token 14 days from its expiry causes one email in the test SMTP server. A restart sends no second email |
| Mode 2 | `opendustpan recovery request`, `ssh-keygen -Y sign`, `opendustpan recovery submit`. The temporary administrator acts. A replay fails. A request without the secret fails |

### Work items

Each item is one session of work. Size: S (under 1000 changed lines), M (1000
to 2500), L (more than 2500, to split again if it grows). The conflict group
names the files that two items in the same group both change, so two items of
one group do not run at the same time. Each item ships its user documentation
page (ADR 214) and its e2e step.

| ID | Item | Size | Conflict group | After |
| --- | --- | --- | --- | --- |
| W1 | The settings resource of ADR 212 decision 7, with "reduces security" in each declaration (accepted design, a prerequisite) | L | settings | none |
| W2 | `session.Assertion`, the three method ports and their suites. `principal.ApplyGroupClaims` with ADR 70 decisions 13 to 22 | M | signin | none |
| W3 | WebAuthn: factor store migrations (both dialects), the adapter on go-webauthn, enrollment routes (O9) | M | store | none |
| W4 | Passkey sign-in for local users and for break-glass accounts (ADR 70 decision 23) | M | signin | W3 |
| W5 | TOTP store adapter of `internal/stepup` (RFC 102 question 5), shared by MFA | M | store | none |
| W6 | Recovery codes | S | store | W3 |
| W7 | Built-in MFA at sign-in: pending sign-in, require for all, grace days, lockout, fresh time | M | signin | W1, W3, W5, W6 |
| W8 | MFA admin: reset route, secret reveal setting, settings | S | settings | W7 |
| W9 | LDAP adapter and sign-in (O3, O10) | M | signin | W2 |
| W10 | LDAP sync job and removals | M | store | W9 |
| W11 | SAML adapter, SP metadata, replay store (O4, O11) | L | signin | W2 |
| W12 | CAS adapter | M | signin | W2 |
| W13 | OAuth 2.0 groups claim and egress check | S | signin | W2 |
| W14 | Trusted header, both modes | M | signin | W2 |
| W15 | SCIM connections, tokens and `/Users` | M | store | W2 |
| W16 | SCIM `/Groups`, `PATCH`, pairing, mass-suspension alert | M | store | W15 |
| W17 | Installation and token IP allowlists, the combine rule, the self-lockout check (O5, O6) | M | authz | W1 |
| W18 | SMTP email sender (`notify.Sender`) and the settings (O3) | S | notify | W1 |
| W19 | Token expiry email job | S | notify | W18 |
| W20 | Token exchange and trust rules of ADR 54 (accepted design, a prerequisite of mTLS) | L | store | none |
| W21 | mTLS: direct mode, trust bundles, exchange with a certificate | M | authz | W20 |
| W22 | mTLS forwarded-header mode | S | authz | W21, W14 |
| W23 | `sshsig` verifier with fuzz tests, shared with RFC 102 (O7) | S | none | none |
| W24 | Mode 2: stored mode and key, the route, the CLI commands, the mode change (O8) | M | store | W23 |
| W25 | `make e2e-identity` with the container tier | M | e2e | W9, W11, W12, W14, W18 |

Order: W1, W2, W3, W5, W20 and W23 can start at once, because they are in
different conflict groups. The `store` group holds each item that adds a
migration, because migration numbers must not collide.

### Proposed terms

The terms go to `GLOSSARY.md` with the ADR.

- **Sign-in method**: a way to prove who a person is: a redirect method, a
  password method, a request method, or a passkey. Avoid "auth provider".
- **Pending sign-in**: the state after the first method and before the built-in
  factor. It holds no rights. Avoid "half session".
- **Recovery code**: a one-use code for a sign-in when a person has lost each
  factor.
- **SCIM connection**: the link of one identity provider to the SCIM routes of
  one home, with its tokens and its paired sign-in provider.
- **Matching rule**: the rule of a SCIM connection that names the subject of
  the paired provider that `externalId` must equal.
- **Trust bundle**: the CA certificates (and CRLs) of a namespace that check a
  client certificate.
- **Signed recovery request**: the document of break-glass mode 2, signed with
  the offline operator key.
- **Offline operator key**: the SSH key of mode 2. Avoid "recovery key", which
  Vault uses for another thing.

### Questions for the owner

Each question has a yes or no answer. "Yes" accepts the recommendation.

| ID | Question | Recommendation |
| --- | --- | --- |
| O1 | Is a passkey the only sign-in of a local user in milestone 4, with no local password (P7)? | Yes |
| O2 | Plain OAuth 2.0 and trusted-header sign-in cannot prove a recent authentication. For these two methods, does a built-in factor (WebAuthn or TOTP) give the fresh authentication of ADR 227? This amends ADR 46 decision 2 for them only (P1). | Yes |
| O3 | Does the server refuse an LDAP or SMTP connection with no TLS, except to a loopback address (P2, P11)? | Yes |
| O4 | Does SAML accept only a sign-in that OpenDustpan starts, and refuse a sign-in that the identity provider starts (P3)? | Yes |
| O5 | Do IP allowlists apply to every credential of a principal: sessions, access tokens, exchanged tokens, SCIM tokens, job tokens and runner tokens (P9)? | Yes |
| O6 | Does the installation IP allowlist apply to break-glass accounts, with the host recovery as the fallback (P9)? | Yes |
| O7 | Does break-glass mode 2 use SSH signatures (`ssh-keygen -Y sign`) with Ed25519 keys, on the standard library (P12)? | Yes |
| O8 | After the first boot, can a site administrator change the recovery mode or the operator key through the API? The change needs a fresh authentication and a reason, waits 24 hours, and sends an alert (P12). | Yes |
| O9 | Can the project add `github.com/go-webauthn/webauthn` (BSD-3-Clause) and its transitive modules? | Yes |
| O10 | Can the project add `github.com/go-ldap/ldap/v3` (MIT) and its transitive modules? | Yes |
| O11 | Can the project add `github.com/russellhaering/gosaml2`, `goxmldsig` (Apache-2.0) and `github.com/beevik/etree` (BSD-2-Clause) for SAML? | Yes |
| O12 | Does a bootstrap admin token stay outside "require MFA" (P7)? | Yes |

The PM decisions of this proposal, each with its precedent: the LDAP check at
sign-in and the hourly sync (P2), the CAS design (P4), the OAuth 2.0 groups
claim (P5), the two trusted-header modes and the groups header (P6), the
WebAuthn, TOTP, recovery code and enforcement defaults (P7), the SCIM subset,
connections, pairing and alert limits (P8), the intersection of IP lists and
the token list at create (P9), the mTLS exchange, the forwarded header and no
CRL download (P10), the SMTP names and the recipients of a service token email
(P11), the request format and flow of mode 2 (P12), the sign-in lockout (P1),
and the two e2e tiers.

## Open questions

The owner answered the first, second and fourth question of the earlier
list on 2026-10-13. These stay open. Each "lead proposal" is a proposed
default from the project lead for the owner to decide. It is not a decision.

- Does "require MFA" apply to the bootstrap admin token? Lead proposal: no.
  The server reads the token from the environment, the operator must retire
  it (ADR 53), and each use writes an audit entry.
- How do IP allowlists at more than one scope combine? Lead proposal: a
  request must pass each list that applies to it, out of the scopes that
  opted in: the installation list, the namespace list and the token list.
- How does the server find that LDAP removed a user? Lead proposal: at the
  next sign-in, and with a sync at a configurable interval.
- Does trusted-header sign-in give groups as well as a user? Lead proposal:
  yes, from a second header that the operator names, off by default.
- How does MFA freshness work with an SSO identity provider, where MFA is the
  job of the provider (ADR 46)? Lead proposal: the server asks the provider
  for a new sign-in when the last one is older than the window of ADR 56.
  Examples are OIDC `max_age` and SAML `ForceAuthn`.
- What are the defaults for the idle timeout and the maximum length of a
  console session? Lead proposal: 15 minutes idle (PCI DSS v4.0.1 8.2.8) and
  12 hours at most (NIST SP 800-63B-4, AAL3). An administrator can change
  both.
- Can an operator change the break-glass mode after the first boot, and how?
  Lead proposal: yes, through an API call by an administrator, with an
  audit entry. The server never changes the mode on its own.
- What does "works once" mean for break-glass mode 1? Lead proposal: one
  sign-in. In that session the operator makes a new administrator or resets
  an MFA. The token stops working when the session ends, or 1 hour after
  the restart if no one uses it.
- How does the audit entry of a break-glass use name a person, as PCI DSS
  v4.0.1 8.2.2 asks? Lead proposal: the recovery request carries the name
  of the operator and a reason, and the audit entry holds both.
- What is the format of the offline operator key and of the signed recovery
  request of break-glass mode 2? Lead proposal: an Ed25519 key pair. The
  request is a small signed document with the installation, a nonce and an
  expiry.

## Owner direction (2026-10-11)

The project owner chose built-in MFA for installs that have no identity
provider (e.g. username+password, ldap, etc). WebAuthn: passkeys, Touch ID, Windows Hello and hardware
keys and TOTP are the options. Recovery codes are shown once at enrollment. An
administrator can reset the MFA of a user. Each reset writes an audit entry
(ADR 41). MFA covers console sign-in, the creation of access tokens, and the
reveal of a secret value (ADR 41). CLI and CI access tokens stay bearer
tokens, with no MFA step on each use. A CLI access token of a person is
created after an MFA sign-in, and it has an expiry. MFA is off by default, for
any number of users. An administrator turns it on. Then the administrator can
require it for all users. With an SSO identity provider (ADR 6), MFA is the
job of the identity provider. OpenDustpan does not add its own MFA step on
top. The related ADRs are ADR 6, ADR 13, ADR 17 and ADR 41.

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Proposal, bullet "MFA covers console sign-in, the creation of access tokens,
  and the reveal of a secret value": "Edit: requiring MFA to reveal secrets
  should be a separate admin toggle."

In the same commit the owner also edited the text of "Owner direction
(2026-10-11)" above. The edits name username and password and LDAP as examples of
installs with no identity provider. They make WebAuthn and TOTP options, and
they name the identity provider an "SSO identity provider". The Proposal above
has the "SSO" edit. It still says "WebAuthn comes first" and "TOTP is the
fallback". The Proposal now follows the edited text. ADR 46 records the result.

## Owner direction (2026-10-13)

The project owner answered questions in this RFC on 2026-10-13. Words in
quotation marks are the words of the owner.

- Question "Which of these belong in the binary, and which are better left to
  the operator's proxy?" The owner answered with the list for milestone 4:
  "LDAP, SAML, CAS, OAuth, SCIM 2.0, Passkeys for local users, Trusted-header
  sign-in, IP allowlists". Each one is an `Authenticator` or a provisioning
  adapter behind a port (ADR 6).
- Question "How long does an MFA sign-in stay valid for the creation of
  access tokens and for the reveal of a secret value?": "Follow PCIDSS/SOC2/ISO
  guidance to pin defaults, and then make it configurable."
- Question "How does a user sign in when they lose every factor and every
  recovery code?" The question gave three options for the case where the
  last administrator loses every factor:
  1. The operator sets a new bootstrap admin token in the configuration or
     the environment and restarts. The token works once, the server audits
     it, and the operator must retire it.
  2. A recovery request signed with an offline operator key.
  3. No break-glass. The operator restores from a backup.

  The owner answered: "Again, follow ISO/SOC/FIPS/PCIDSS guidance if any
  precedent exists as a default. and then make it configurable to on
  first-boot to support either 1, 2, or 3. Default to 1 if no guidance exists
  from standards bodies."

The research doc `docs/research/m4-identity-defaults.md` holds the findings
for the defaults.

Second round, through the project lead, on 2026-10-13:

- Question "What does \"OAuth\" cover: OIDC only, or also plain OAuth 2.0
  providers?": "Any OAuth 2.0 provider". ADR 55 records how this fits ADR 6:
  a provider with no OIDC needs a configured user-info endpoint and a claim
  mapping to identify a person.
- Question "What does a SCIM or LDAP removal do to the user's tokens and
  sessions?": "Disabled by default, with opt-in auto-revoke." The project
  lead reads this as follows. A removal disables the sessions and the access
  tokens of the user, and the server keeps them for review. An administrator
  setting turns on revocation at once instead.
- Question "Where do IP allowlists apply?": the project lead first gave a
  summary: per installation, per namespace and per access token, for access
  tokens and console sign-in. The owner then corrected it: "IP allowlists
  need to be enabled by the admin first, with opt-in per namespace/access
  token/etc". So IP allowlists are off by default. An administrator of the
  installation turns the feature on. Then each scope opts in on its own:
  the installation, each namespace and each access token. When the feature
  is on, the lists apply to console sign-in and to access-token requests.

## Outcome

The part on built-in MFA is accepted as ADR 46 on 2026-10-12.

Accepted on 2026-10-13:

- ADR 55: LDAP, SAML, CAS, OAuth, SCIM 2.0, passkeys for local users,
  trusted-header sign-in and IP allowlists are in the binary in milestone 4.
  Each is an adapter behind a port. OAuth covers any OAuth 2.0 provider. IP
  allowlists are off by default. An administrator turns them on, and then
  the installation, each namespace and each token opt in. A SCIM or
  LDAP removal disables the sessions and tokens of the user, and revocation
  at once is opt-in.
- ADR 56: an MFA step stays valid for 15 minutes by default for token
  creation and for secret reveal. No standard sets a number for MFA
  freshness. The number comes from the idle limit of PCI DSS v4.0.1 8.2.8,
  by analogy. An administrator sets each window.
- ADR 57: three break-glass modes. The operator chooses one at first boot.
  Mode 1, the one-time bootstrap admin token, is the default, because no
  standard names a preferred option.

The questions in "Open questions" have no outcome yet.
