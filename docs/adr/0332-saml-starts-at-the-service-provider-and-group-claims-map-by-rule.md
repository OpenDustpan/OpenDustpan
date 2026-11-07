# 332. SAML starts at the service provider, and group claims map by rule

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026. Answers question O4 of [RFC 21](../rfc/0021-directory-sync-and-sign-in.md) and the held decision 12 of ADR 320

Changes:

- Changes ADR 320 decision 12: the form of the group mapping is a rule for each provider, with explicit single mappings for exceptions.
- Extends ADR 321 decisions 13, 14, 15 and 22: a sign-in that OpenDustpan starts is the normal path. An optional sign-in that the identity provider starts uses the checks of decisions 14 and 15, with a shorter age. It is never a fresh authentication.
- Changes ADR 321 decisions 19, 30, 33 and 38: group claims map to groups by the rule of this ADR.
- Extends ADR 70 decisions 12, 14, 16 and 22: a membership from a rule has the source `claim` and records the rule. The rules for protected groups stay. The rules are a sensitive setting.

## Context

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) question O4 asks if SAML accepts only a sign-in that OpenDustpan starts. A sign-in that the service provider starts (an "SP-started" sign-in) has an `AuthnRequest` with an ID. The server checks `InResponseTo`. A sign-in that the identity provider starts (an "IdP-started" sign-in) has no request, so it has no binding to a browser. A stolen assertion can then sign in another browser. ADR 321 waited for the owner on this point.

[ADR 320](0320-one-sign-in-path-for-every-sign-in-method.md) decision 12 says that a mapping turns group values into groups, and that the owner decides its form. Until then no mapper exists, and each source has no mapping. The port `principal.GroupMapper` fits an explicit map from a value to a group and a match that a group turns on for one provider.

[ADR 70](0070-act-as-reach-idp-groups-and-break-glass.md) decisions 12 to 22 give the rules for the memberships that an identity provider makes. This ADR does not change them.

## Decision

Decided by the project owner on 7 November 2026.

1. **SAML sign-in starts at OpenDustpan (owner, 2026-11-07).** The normal SAML sign-in is SP-started. The server makes the `AuthnRequest` and checks `InResponseTo`.
2. **A portal tile is a bookmark (owner, 2026-11-07).** A tile in an identity provider portal is a bookmark to the SP-started URL of OpenDustpan. It starts an SP-started sign-in. It sends no unsolicited assertion.
3. **IdP-started SAML is a setting (owner, 2026-11-07).** True IdP-started SAML accepts an assertion that has no request. It is a setting of each SAML provider. It is off by default.
4. **IdP-started SAML has four required checks (owner, 2026-11-07).** When the setting is on, the server accepts an unsolicited assertion only with all of these checks:
   - The assertion ID is one-time. The server stores it and refuses a second use.
   - The assertion is not older than a maximum age of about 5 minutes.
   - The `Audience` check passes.
   - The `Recipient` check passes.

   An assertion that fails one check is refused. The checks of ADR 321 decision 14 apply too.
5. **IdP-started SAML is never a fresh sign-in (owner, 2026-11-07).** A sign-in that starts at the identity provider never counts as a fresh authentication. A change that needs a fresh authentication asks for a new SP-started sign-in with `ForceAuthn="true"` (ADR 321 decision 22).
6. **Group mapping is a rule for each provider (owner, 2026-11-07).** Each provider has a rule: a trusted claim pattern. A claim value that matches the pattern is linked to a group. The link names the provider and the claim value. It never names the group by name alone.
7. **Explicit single mappings handle exceptions (owner, 2026-11-07).** An explicit mapping links one claim value of one provider to one group. Use it for a value that the pattern does not cover.
8. **The rules are a sensitive setting (owner, 2026-11-07).** A change to a rule or to an explicit mapping is a sensitive setting. It writes an audit entry and needs a fresh authentication (step-up), as ADR 70 decision 22 says for a claim mapping.
9. **SCIM comes later (owner, 2026-11-07).** This ADR adds no SCIM. When SCIM comes, it uses the same mapping, as ADR 320 decision 13 says.

## Consequences

- The SAML adapter of ADR 321 has one normal path. The path without a request exists only where an administrator turns it on for one provider.
- The API shows the effective value of the IdP-started setting of each provider. The default is off.
- A group that two providers use has one link for each pair of provider and claim value. A claim value of one provider never reaches a group that another provider linked.
- ADR 70 decisions 13 to 16 still apply. A rule does not add a member to a protected group unless the group trusts the identity provider.
- This ADR does not decide the syntax of the claim pattern or the exact maximum age of an assertion. It does not decide the shape of the audit entries. A later decision does.
