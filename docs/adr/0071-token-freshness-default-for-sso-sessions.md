# 71. Token freshness default for console sessions from an SSO provider

Status: accepted · 2026-10-16

## Context

[ADR 56](0056-mfa-freshness-for-token-creation-and-reveal.md) sets a
freshness window of 15 minutes for token creation and secret reveal when
built-in MFA covers the operation. It keeps open how freshness works with an
SSO identity provider, where MFA is the job of the provider.
[ADR 70](0070-act-as-reach-idp-groups-and-break-glass.md) keeps the same
question open for the changes that need a fresh MFA step.

A console session comes from a sign-in through an SSO identity provider. The
server cannot see the MFA step of the provider. It can see the time of the
authentication (`auth_time`), and it can ask the provider for a new
authentication (`max_age=0`). The session policy has the setting
`tokenFreshnessSeconds` for this window.

## Decision

The project owner decided on 2026-10-16:

1. **15 minutes by default.** The default token freshness window for console
   sessions from an SSO provider is 15 minutes. This is the same number as
   ADR 56 decision 2.
2. **An operator can set 0.** A window of 0 turns the check off. The other
   values are 60 seconds to 7 days, as the session policy allows.

## Consequences

- A token create or rotation from a console session with an authentication
  older than 15 minutes answers 401 and names the path of a new sign-in. The
  console sends the browser to that path.
- A change of the session policy from a console session also needs an
  authentication inside the window, so an old session cannot turn the check
  off.
- An operator who sets 0 accepts a session of any age for these operations.
  The server writes an audit entry for each change of the window, with the
  session ID when a console session makes the change.
- A console session can still import and restore a stack. The owner
  said that this does not change.
