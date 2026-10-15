# 69. Token lifetime and session defaults

Status: accepted · 2026-10-14

## Context

ADR 53 decision 2 lets an administrator set the maximum lifetime of an
access token, and leaves the default open. RFC 21, RFC 20 and
[RFC 68](../rfc/0068-installation-settings.md) list proposals of the lead
for the session timeouts, the expiry warnings and the lifetime of an
exchanged workload token.

The project owner decided on 2026-10-14:

- About token expiry: "the user should be able to pick the expiry date with
  a little date picker. This can max can default 365 days unless an admin
  overrides it (similar to GitLab) unless any of those security standards
  mandates a lower number, then that becomes the default."
- About why: "Sometimes you need a long-lived token for a legacy system that
  can't do OIDC/mTLS so 365 days is nice to reduce operational toil.
  Sometimes an engineer just needs to quickly test something with a
  throwaway token that will expire after a day if they forget to revoke it."
- About the other proposals: "If they clear SOC/PCI/ISO/etc then that's
  fine."

The research note `docs/research/m4-identity-defaults.md` gives these
numbers:

- PCI DSS v4.0.1 requirement 8.2.8: a session that is idle for more than
  15 minutes needs a new sign-in.
- NIST SP 800-63B-4 section 2.3.3 (AAL3): a new sign-in after at most
  12 hours.
- PCI DSS v4.0.1 requirement 8.3.9 asks for a change of a password every
  90 days only when the password is the single factor of a person. An
  access token in OpenDustpan comes from a sign-in with MFA where ADR 46
  requires it (ADR 53 decision 10).
- PCI DSS v4.0.1 requirement 8.6.3 lets the entity set the change period of
  the credentials of system and application accounts.
- ISO/IEC 27002:2022 and the SOC 2 Trust Services Criteria set no number.

No standard that the note read sets a maximum lifetime for an access token.

## Decision

1. **The person picks the expiry date.** The API takes an expiry date when
   it makes a token. The console shows a date picker, with short choices
   such as 1 day next to longer ones up to the maximum. The server refuses a
   date after the maximum lifetime.
2. **The maximum lifetime is 365 days by default.** It applies to personal
   tokens and to service tokens. An administrator can set another maximum
   for the installation. A namespace can set a shorter maximum for the
   tokens of its principals (ADR 67).
3. **The guide names the PCI case.** The security model guide says that an
   auditor can treat a personal token as a single-factor password under PCI
   DSS requirement 8.3.9. An operator who wants that reading sets a maximum
   of 90 days.
4. **Console sessions.** A console session ends after 15 minutes with no
   activity, and after 12 hours in all. Both are settings.
5. **Expiry warnings.** The warnings of ADR 53 decision 11 start 14 days and
   again 3 days before the expiry. Both are settings.
6. **An exchanged workload token lives 1 hour.** The token that a workload
   gets in an exchange (ADR 54) expires after 1 hour. It is a setting.

## Consequences

- A token with no expiry is not possible unless an administrator removes
  the maximum.
- The defaults pass PCI DSS v4.0.1 requirements 8.2.6 and 8.2.8, and NIST SP
  800-63B-4 AAL3 for the session length. ISO/IEC 27001 and SOC 2 ask for a
  policy and do not set numbers.
- RFC 68 lists these settings with the defaults of this ADR.
