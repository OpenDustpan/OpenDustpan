# 53. Access token life cycle, inactivity disable and expiry warnings

Status: accepted · 2026-10-13. The SMTP settings decided by ADR 212

## Context

ADR 6 says that OpenDustpan issues its own access tokens. It sets no kinds,
lifetimes or reports. [RFC 20](../rfc/0020-access-tokens.md) collects the
questions. On 2026-10-11 the project owner chose a token life cycle: a
last-used time and an expiry on each access token, an automatic disable
after a period of no use, service tokens, rotation, and a prompt to retire
the bootstrap admin token.

On 2026-10-12 the owner wrote: "The maximum token lifetime and which ones
users can issue must be configurable by the admin (I guess the latter just
becomes part of the FGA model)".

On 2026-10-13 the owner answered two more questions:

- The inactivity default: "What do ISO/SOC2/PCIDSS recommend? Follow that as
  the default but admins can override this to any value including
  infinite." This replaces the limit of 90 days or fewer from 2026-10-11.
- The expiry warning: "Event/Webhook for this goes to M5., API field +
  console banner, Email, CLI warning".
- What the inactivity disable covers: "Configurable. Default to tokens +
  accounts". The owner also exempted break-glass by default and asked that
  each of these is a setting. RFC 20 quotes the full answer.

The research note `docs/research/m4-identity-defaults.md` found one number.
PCI DSS v4.0.1 requirement 8.2.6 says: "Inactive user accounts are removed or
disabled within 90 days of inactivity." No source that the note read sets a
number for an access token or for a credential of a machine. ISO/IEC
27002:2022 and the SOC 2 Trust Services Criteria give no number in the parts
that the note could read. NIST SP 800-53 Rev. 5 AC-2(3) leaves the period to
the organization.

## Decision

1. **Last-used time and expiry.** Each access token records the time of its
   last use. Each access token has an expiry.
2. **A maximum lifetime.** An administrator sets the maximum lifetime of an
   access token. The server refuses a token with a longer lifetime.
3. **Who can issue which token is part of the authorization model.** Each
   kind of token has its own action. The grants of ADR 52 decide which
   principal can issue which kind. The owner wrote this with "I guess".
4. **Inactivity disable, 90 days by default.** The server disables an access
   token that no principal used for 90 days. It also disables a user account
   with no sign-in and no token use for 90 days. The number comes from PCI
   DSS v4.0.1 requirement 8.2.6, which names user accounts. No standard that
   the note read sets a number for access tokens, so the server uses the same
   number for tokens by analogy.
5. **The break-glass credential is exempt by default.** The inactivity
   disable skips the break-glass credential of ADR 57. NIST SP 800-53 Rev. 5
   AC-2 says that some "accounts of last resort" are not subject to
   automatic disabling.
6. **Each part is a setting.** An administrator can set any period, or turn
   the inactivity disable off. An administrator can turn it on or off for
   access tokens, for user accounts and for the break-glass credential
   apart. The server writes an audit entry for each change.
7. **Service tokens.** A service token serves a machine that cannot sign in
   to the console. It belongs to a principal that is not a person. Workload
   identity comes first for machines (ADR 54).
8. **Rotation.** A rotation call issues a new access token and sets an end
   time for the old one. A principal can use the old token until that end
   time.
9. **Retire the bootstrap admin token.** On the first sign-in with the
   bootstrap admin token (ADR 6), the console and the API ask the operator
   to make a personal admin token and to retire the bootstrap token. This
   helps with ISO/IEC 27002:2022 control 5.17.
10. **A personal CLI access token comes from a sign-in.** A person creates it
    after a sign-in, with MFA where ADR 46 requires MFA. It has an expiry.
11. **Expiry warnings in milestone 4.** Before an access token expires, the
    server warns in four ways:
    - a field in the API response for the token,
    - a banner in the console,
    - an email to the owner of the token. This needs SMTP settings.
    - a warning in the CLI.
12. **Events and webhooks in milestone 5.** An expiry event and its webhook
    come with RFC 23 in milestone 5.
13. **API and audit.** Every token operation has a REST API and an action
    (ADR 17). Each operation writes an audit entry (ADR 41).

## Not decided here

[RFC 20](../rfc/0020-access-tokens.md) keeps these open:

- Whether the settings of decisions 2 and 6 belong to the installation, to
  a namespace, or to both.
- The default of the maximum lifetime, and how long before the expiry the
  warnings start.
- Which CLI shows the warning: the `opendustpan` CLI, or the tool CLIs. A
  tool CLI shows a server warning only if its protocol has a place for one.
  This needs a check with `make e2e`.
- Where the SMTP settings live, and whether they are run-time settings
  (ADR 17).
- Which roles can rotate or revoke the access token of another principal.
- (e2e) Which TFE token endpoints (organization, team) the TFE-compatible
  API must serve.

## Consequences

- The server needs a job that finds tokens and user accounts with no use for
  the set period and disables them. Each disable writes an audit entry.
- The email warning needs an SMTP client and its settings. Email is a new
  channel for the server.
- An update of the last-used time on each request costs a write. The server
  can limit how often it writes the time.
- Token kinds, last-used times and end times need numbered migrations in
  each SQL dialect (CLAUDE.md).
