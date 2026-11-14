# 70. Act-as reach, identity provider groups and break-glass accounts

Status: accepted · 2026-10-15. The `server:` action names superseded in part by ADR 210. Decisions 11, 23 and 28 superseded in part by ADR 216: the sign-in of a namespace, and support access. The fresh MFA step of decisions 16, 17 and 23 uses the window of ADR 227. Decisions 26 and 27 refined by ADR 245: the host command, its reason and its review

Changed by:

- ADR 142 changes decision 26: the review after use of a break-glass session.
- ADR 142 extends decision 13: server:review-break-glass is a sensitive
  action.
- ADR 210 changes: the route paths and the server: action names.
- ADR 216 changes decisions 11, 23, 24 and 28: sign-in of a namespace,
  break-glass of a namespace, support access.
- ADR 222 extends decisions 16, 17 and 23: the fresh step uses the one window.
- ADR 227 extends decisions 16, 17 and 23: the fresh MFA step uses the one
  window.
- ADR 245 changes decisions 26 and 27: the host command, its reason and its
  review.
- ADR 306 changes decisions 24, 25 and 26: a site admin may disable a break-glass principal, and the rule of the sweep.
- ADR 306 extends decision 21: the limit and the window of the mass-suspension alert.
- ADR 320 extends decision 12: LDAP memberships have the source `ldap`. SAML, CAS and trusted-header persons have their own principal source.
- ADR 321 extends decision 19: an LDAP sync applies a disabled entry at once. A search that misses more persons than a bound waits for a site administrator.
- ADR 332 extends decisions 12, 14, 16 and 22: a claim rule records its rule. The rules are a sensitive setting.
- ADR 354 changes decision 13: a delegation is a new edge kind.
- ADR 369 changes decision 9: a trust rule in person mode needs
  `server:manage-principals`.
.
## Context

Issue #531 showed that an access token can give its maker authority that no
grant gave. ADR 67 fixed that case for service principals. The project owner
then asked for a formal model of access, so that the same kind of flaw cannot
come back.

[RFC 70](../rfc/0070-authorization-model-findings.md) reports a study of the
principals, groups, roles, grants, tokens, trust rules and restores of ADRs
51 to 57, 60, 64, 65 and 67 to 69. The study checked four properties. None of
them names a role or an issue:

- **P1 accountability.** What a person can do with each credential that it
  holds is what the access review says that it can do.
- **P2 no amplification.** No change makes anyone able to do, or to act as,
  more than the actor holds.
- **P3 catalog claims.** A role means what its description says, after every
  act-as edge.
- **P4 confinement.** A right acts only on the kinds of object that it was
  meant for.

The checks found #531 again with no hint, and found 14 more paths. RFC 70
lists eight items that need a decision, D1 to D8.

A literature review on escalation and lockout through an identity provider
added evidence for D7. It found these points:

- Every product that it surveyed keeps an anchor for its top role that does
  not depend on the identity provider.
- Attackers changed identifying attributes, added a trusted identity provider
  or added credentials to a service principal. A rule that only blocks group
  adds misses these paths.
- A brake that holds removals conflicts with NIST SP 800-63C. A model that
  removes the rights at once and keeps the record for a fast restore does not.

The project owner decided D1 to D8 on 2026-10-15. This ADR records those
decisions. For D7 the owner set two limits: "The default, documented
behaviour should just be the safe one", and "Whatever we decide on, it needs
to work on hosted, and it needs to be standards compliant."

### Standards

This ADR cites only control text that the literature review read in the
primary source:

- NIST SP 800-53 Rev. 5: AC-2, AC-2(2), AC-2(4), AC-2(7), AC-6(5), AC-6(7),
  AC-6(9) and IA-2(1).
- NIST SP 800-63C.
- RFC 7643 and RFC 7644 (SCIM 2.0).
- PCI DSS v4.0.1: 7.2.2, 7.2.3, 7.2.4, 8.2.2, 8.2.4, 8.2.5, 8.2.6, 8.4.1,
  8.6.1, 8.6.2, 10.2.1.2 and 10.2.1.5.
- ISO/IEC 27001:2022 Annex A: A.5.15, A.5.16, A.5.18, A.8.2 and A.8.5.
- ISO/IEC 27002:2022: 5.16, 5.17, 5.18 and 8.2.
- AICPA Trust Services Criteria (SOC 2): CC6.1, CC6.2 and CC6.3.
- CIS Controls v8.1: Safeguards 5.3, 5.4, 6.2, 6.5 and 6.8.
- CSA Cloud Controls Matrix v4.1: IAM-07, IAM-09, IAM-10, IAM-15 and LOG-07.
- Brucker and Petritsch, "Extending Access Control Models with
  Break-glass" (SACMAT 2009).

None of these texts names an identity provider, a minimum number of
administrators, two break-glass accounts or a brake on bulk removals. Those
parts of this ADR come from product practice, not from a standard.

## Decision

### Terms

1. **Reach and act-as edges.** The reach of a principal is the set of actions
   on objects that the principal can do. It comes from these sources:
   - the grants of the principal,
   - the grants of the groups of the principal,
   - the grants of each principal that it can act as, through an act-as edge.

   An act-as edge is a right that lets one principal use the authority of
   another. The edges are `principal:manage-tokens` on a principal, and the
   right to write a trust rule that names a principal (decision 10). One
   reach covers another when it holds each of its actions on each of its
   objects.

### Act-as reach (D1, #551)

2. **Reach limits an installation-wide token right.** A holder of
   `principal:manage-tokens` on the installation can make or rotate a token
   of a service principal only when the grants of that principal fit inside
   the reach of the holder. Otherwise the server refuses the request. A
   site administrator holds every action, so the check always passes for it.
   An account administrator can no longer act as a principal that holds an
   operator action. This keeps ADR 65 true after every act-as edge.
3. **A grant of an act-as right needs the reach of its target.** These
   changes need a granter whose reach covers the reach of the target
   principal:
   - a grant of a role with `principal:manage-tokens` on that principal, to a
     principal or to a group,
   - a new member of a group that holds such a grant.

   This is the rule that the owner set for principals of a namespace. It
   now applies to the whole installation. It comes in addition to the action
   that ADR 67 decision 4 asks for.
4. **The catalog says what account-administrator can do.** The description
   of `account-administrator` says that it makes the tokens of each service
   principal whose grants fit inside its own reach. The role holds no
   operator action, directly or through an act-as edge.

### Tokens (D2, #552, and D3, #555)

5. **Each person makes only its own personal tokens.** No principal can
   make or rotate a personal token of another person. A holder of
   `server:manage-tokens` can revoke the personal token of any person. It
   cannot make one.
6. **A service token outlives the right of its maker, and the access review
   shows it.** When the maker of a service token loses the last act-as path
   to the principal of the token, the server keeps the token. The access
   review flags it. The flag names the token, its principal, its maker and
   the time that the maker lost the right. The same flag applies to a token
   that decision 2 would refuse today, because the principal got more grants
   after its maker made the token.

### Restores (D4, #553)

7. **The access tables restore only with their own action.** A new action,
   `server:restore-access`, lets a restore replace the access tables. Of the
   built-in roles, only `site-administrator` holds it. A restore by a caller
   without it replaces the other data and keeps the current access tables.
   The access tables are the principals, groups, memberships, roles, grants,
   access tokens and trust rules.
8. **A setting can change this.** An installation setting can let each
   holder of `server:restore` replace the access tables. It is off by
   default. A change of the setting writes an audit entry.

### Trust rules (D5, #554)

9. **A trust rule names only a principal that its author can act as.** To
   create a trust rule, or to change the principal that it names, the author
   must hold `principal:manage-tokens` on that principal. Decision 2 applies
   when the author holds the action on the installation.
10. **The author of a trust rule has an act-as edge.** The access review and
    the reach of decision 1 count the author of each trust rule as a
    principal that can act as the principal of the rule.

### Sign-in settings (D6)

11. **`server:manage-sign-in` gates the sign-in settings.** It is the action
    for changes to the sign-in methods, SCIM, MFA and the session settings.
    It is also the action for the changes of decision 22. Only
    `site-administrator` holds it. The operator family never holds it.

### Identity provider groups (D7, #557)

12. **Each membership records its source.** A membership comes from a local
    change, from SCIM or from a group claim at sign-in. The membership
    records the source and the identity provider or SCIM token.
13. **No admin role comes from the identity provider by default.** A group is
    protected when its reach holds a sensitive action that is not confined
    to one namespace. These are the sensitive actions:
    - access management on the installation,
    - delegation, that is, an act-as right on a principal of the installation,
    - the sign-in settings,
    - each action of the operator family,
    - the configuration of identity providers and of SCIM.

    The server computes protection over four kinds of edge: create a
    principal, issue a credential, grant a role and act as a principal.
14. **The identity provider cannot add a member to a protected group.** The
    server refuses the add. A SCIM request gets a SCIM error (RFC 7644). A
    group claim at sign-in does not add the membership, and the server
    writes an audit entry. RFC 7643 section 4.2 leaves the authorization that
    a group gives to the service provider.
15. **A switch closes any group.** A person who manages a group can set it to
    "the identity provider may not add members". The switch writes an audit
    entry.
16. **A group can trust the identity provider.** A person can set a group,
    protected or not, to "trusted from the identity provider". Then
    decision 14 does not apply to the group. The change needs a fresh MFA
    step (ADR 56) and a person whose reach covers the reach of the group. It
    writes an audit entry.
17. **Full sync is an installation setting.** A site administrator can set
    the whole installation to full sync from the identity provider. The
    identity provider is then trusted for every group, and each removal is
    final. The change needs a fresh MFA step and writes an audit entry. Full
    sync never changes a break-glass account (decision 26).
18. **One rule for SCIM and for sign-in claims.** Decisions 13 to 17 and 19
    to 21 apply in the same way to a SCIM change and to a group claim at
    sign-in.
19. **A termination applies at once.** When the identity provider
    deactivates a user, or sends a SCIM `DELETE` for it, the server disables
    the user at once, as
    ADR 55 decision 9 says. Decision 20 never delays a termination. NIST SP
    800-63C asks the relying party to act on a termination signal, and PCI
    DSS 8.2.5 asks the service to revoke access at once.
20. **A removal from a protected group suspends the membership.** When the
    identity provider removes a member from a protected group, these steps
    occur:
    - The member loses the rights of the group at once.
    - The server keeps the membership record as "suspended by the identity
      provider" for 30 days. After 30 days, the removal is final.
    - A site administrator, or a break-glass account, can restore one
      suspended membership, or all of them, with one action.
    - The server pins a restored membership. The identity provider does
      not manage it until a person unpins it.
    - A restore is a new privileged grant. It needs a reason that the person
      types, and it writes an audit entry (PCI DSS 8.2.4 and 10.2.1.5).

    With full sync (decision 17), a removal is final at once.
21. **Mass suspensions raise an alert.** The server counts the member
    removals that each operation causes, over a rolling window. A count above
    a limit raises an alert. One SCIM `replace` or `remove` with no filter can
    remove all members of a group (RFC 7644 section 3.5.2), so the server
    counts members, not requests.
22. **These rules close the side doors.**
    - A change to an identifying attribute of a member of a protected group
      is a protected action. Examples are the user name, the email, the
      `externalId` and the federated subject. Account linking is a
      protected action too.
    - The server never links a federated sign-in to an account by an
      attribute that can change.
    - A change to a trusted identity provider, a claim mapping, an account
      matching rule or a SCIM token is a sensitive action on the
      installation. Each change raises an alert.
    - A grant that makes a group protected while the group has members from
      the identity provider fails, unless the same request decides the trust
      of the group (decision 16).
    - A credential issue to a principal in a protected group is a protected
      action.
    - The server raises an alert before a SCIM token expires.

    A protected action needs a reach that covers the reach of the protected
    group.

### Break-glass accounts (D7)

23. **Each installation has break-glass accounts, the hosted service too.**
    An installation has at least two break-glass accounts. Each one belongs
    to one named person. No account depends on the identity provider. Each
    account signs in with phishing-resistant authentication: a passkey or a
    hardware key (NIST SP 800-53 IA-2(1)). On the hosted service, they hold
    `account-administrator` of the tenant. The site administrator of the
    hosted operator is a separate principal.

    A self-hosted installation can turn break-glass off. The default is on.
    A site administrator of a self-hosted installation can turn off the
    break-glass requirement. The installation then has no break-glass
    accounts and no guard, and its administrators come only from the managed
    accounts of the installation. The change needs a fresh MFA step and
    writes an audit entry. The setup and the console say that the operator
    accepts the risk of a lockout. The admin access report shows the
    setting. The hosted service cannot turn break-glass off. This keeps ADR
    57 mode 3.
24. **A guard keeps two working paths.** The first setup cannot finish
    without two break-glass accounts. The server refuses each change that
    leaves fewer than two working paths, a restore included. It counts only
    an account that has these four properties: enabled, not expired, not
    blocked by a policy, and not dependent on the identity provider. A
    rotation of a break-glass credential is atomic.
25. **Break-glass accounts are accounts of last resort.** NIST SP 800-53
    AC-2 names accounts of last resort, which are not subject to automatic
    disabling. No job disables a break-glass account. After a break-glass
    sign-in, the elevated session has a time limit, and the server then
    revokes it (AC-2(2), ISO/IEC 27002 8.2).
26. **The server records each use.** Each break-glass sign-in needs a reason
    that the person types. It raises an alert. The audit log records each
    action of the session (AC-2(4), AC-6(9), PCI DSS 8.2.2, 8.6.1 and
    10.2.1.2). When the audit log cannot record an action of a break-glass
    session, the server refuses the action. Full sync (decision 17) cannot
    disable or change a break-glass account.

    Each use ends with a clean-up. After a break-glass session ends, the
    server marks the account "used, not reviewed". A second
    administrator reviews the audit entries of the session and records the
    review. The person who used the account rotates its credential. The
    admin access report lists each use that has no review. Approval before
    use cannot work in a lockout, so this review after use takes the place
    of the management approval that CSA CCM IAM-10 and IAM-15 ask for
    (Brucker and Petritsch: monitor, then clean up).

    Each account has a regular test. The server reminds the
    administrators to test each account every 30 days. A test is a real
    sign-in, so that no account is inactive for more than 45 days (CIS
    Safeguard 5.3) or 90 days (PCI DSS 8.2.6). The period is a setting of at
    most 45 days.

### Recovery (D7)

27. **A self-hosted operator recovers with a host token.** The host token
    makes a temporary administrator for one named person, never a shared
    account, so that each action is attributable to a person (PCI DSS
    8.2.2, ISO/IEC 27002 8.2). The token is not stored in a configuration
    file (PCI DSS 8.6.2). The console shows a banner for that principal, and
    it expires at a fixed time. The path works without the
    identity provider, without DNS and without the public listener. The
    audit log records each step.
28. **The hosted service recovers through support.** The provider verifies
    the identity of the requester out of band before it issues a new
    credential (ISO/IEC 27002 5.17). The tenant audit log records the recovery.

### Admin access report (D7)

29. **A periodic admin access report.** The API answers it first. Email and
    webhook delivery come later. The report holds:
    - the holders of each sensitive installation action, and the path that
      gives each one,
    - the protected groups, and each group that trusts the identity
      provider, with the person who set it,
    - the break-glass accounts, with the last use and the last test,
    - the suspended memberships and the refused adds since the last report,
    - the changes since the last report.

    A reviewer acknowledges each report through the API, with notes on the
    access that the reviewer removed. The server records the acknowledgment. This
    supports the review of privileged accounts (AC-6(7), PCI DSS 7.2.4,
    ISO/IEC 27002 5.18, SOC 2 CC6.3) and the record of privileged role
    changes (AC-2(7)).

### A model checker (D8)

30. **A separate repository holds the model.** `OpenDustpan/authz-model`
    holds three parts:
    - a Datalog gate (Souffle) for P1 to P4,
    - a bounded temporal model (Alloy 6),
    - a relation graph in the OpenFGA DSL, from the OpenFGA CLI.

    Alloy, SAT4J and OpenJDK run in the CI of that repository only.
    OpenDustpan never ships them.
31. **OpenDustpan imports one module in tests.** The module is Go with the
    standard library only. OpenDustpan imports it only from test code.

### Changes to earlier ADRs

32. **This ADR changes these parts of earlier ADRs.** The other parts stay.

    | ADR | Part | Change |
    | --- | --- | --- |
    | 53 | Decision 5 | A break-glass account is always exempt from the inactivity disable, not only by default (decision 25). |
    | 53 | Decision 6 | The inactivity disable has no setting for break-glass accounts. The other settings stay. |
    | 53 | Decision 10 | Only the person makes its personal token (decision 5). |
    | 53 | Not decided here, "which roles can rotate or revoke" | A holder of `server:manage-tokens` revokes. Nobody rotates the personal token of another person. |
    | 54 | Decision 5 | A trust rule names only a principal that its author can act as (decisions 9 and 10). |
    | 54 | Decision 8 | A trust rule change needs `principal:manage-tokens` on the named principal, beside the action of the route. |
    | 55 | Decision 4 | SCIM groups follow decisions 12 to 22. |
    | 55 | Decision 9 | A termination disables at once (decision 19). A removal from a protected group suspends the membership (decision 20). With full sync, a removal is final. |
    | 55 | Decision 11 | Changes to sign-in methods and SCIM need `server:manage-sign-in` (decision 11). Identity provider, claim mapping, matching rule and SCIM token changes raise an alert (decision 22). |
    | 67 | Decision 2 | `principal:manage-tokens` on the installation acts only on a principal inside the reach of the holder (decision 2). |
    | 67 | Decision 3 | The server compares reach in two places. It checks a holder on the installation when it makes a token (decision 2). It checks the granter of an act-as right (decision 3). It does not check a holder on one principal when it makes a token. |
    | 67 | Decision 4 | The grant also needs the reach of its target (decision 3). |
    | 67 | Decision 5 | An installation administrator no longer makes the personal token of another person (decision 5). |
    | 67 | Decision 6 | The access review also flags the tokens of decision 6 and lists the authors of trust rules (decision 10). |

    These parts of other ADRs also change:

    | ADR | Part | Change |
    | --- | --- | --- |
    | 38 | Decision 6 | Without `server:restore-access`, the apply step keeps the access tables (decision 7). |
    | 52 | Consequence on groups from an identity provider | Groups from an identity provider follow decisions 12 to 22. |
    | 56 | Decisions 3 and 4 | The MFA freshness settings need `server:manage-sign-in` (decision 11). |
    | 57 | Decision 4 | The recovery token makes a temporary administrator (decision 27). |

## Not decided here

- The time limit of the elevated break-glass session, and the drill reminder
  channel before email exists.
- The limit and the window of the mass-suspension alert (decision 21).
- What happens to the members from the identity provider when a request
  decides that a group does not trust it (decision 22).
- How a fresh MFA step works with an SSO identity provider (ADR 56, "Not
  decided here"), for decisions 16 and 17. The step should not depend on the
  identity provider.
- How the host token of decision 27 relates to ADR 57 modes 1 and 2.
- Whether the access tables of decision 7 also hold the sign-in settings and
  the break-glass accounts. How a restore keeps rows that name units or
  namespaces that the backup does not hold.
- The channel of an alert before RFC 23 adds events and webhooks.
- Who sets the identity provider settings of a tenant on the hosted service,
  where only the hosted operator holds `server:manage-sign-in`.
- The period of the admin access report.
- The name of the flag of decision 6, and when it clears.

## Consequences

- The authorizer needs a reach function. It computes reach over grants,
  group memberships and act-as edges. The token service, the grant service,
  the membership service and the trust rule service call it.
- The built-in role catalog changes the text of `account-administrator`. The
  catalog gets the action `server:restore-access`.
- The route that makes a personal token for another person goes away. The
  console removes its control. The revoke route stays. This removes a field
  or a route that a released console may use, so the API deprecates it first.
- Memberships need a source, a state (`active`, `suspended`) and a pin. This
  needs numbered migrations in each SQL dialect.
- The SCIM adapter and each sign-in adapter call one membership function that
  applies decisions 13 to 22. The e2e suite drives a real or a test identity
  provider through removals, attribute changes and token expiry.
- The first setup asks for two break-glass accounts with passkeys or
  hardware keys. An installation without them cannot finish its setup,
  unless a self-hosted operator turns break-glass off (decision 23). An
  upgrade of an existing installation needs a path to add them.
- The restore code splits the access tables from the other data.
- `GLOSSARY.md` gets the terms reach, act-as edge, protected group, full sync,
  suspended membership, pinned membership, break-glass account and admin
  access report. The word "break-glass" now names accounts as well as the
  recovery modes of ADR 57, so the glossary flags the two meanings.
- `OpenDustpan/authz-model` must carry a license that `make trivy` accepts.
  Its Go module goes into `go.mod`, so `make licenses` and `make vuln` check
  it.
- The protections in decisions 13 to 22 do not apply to a group that trusts
  the identity provider, or to any group under full sync. The owner allows
  this for every group, including a group that reaches the identity provider
  configuration. The literature review advises against trust for such a
  group. The admin access report shows these choices, and the break-glass
  accounts stay outside them.
