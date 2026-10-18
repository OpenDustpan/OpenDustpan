# 56. MFA freshness for token creation and secret reveal

Status: accepted · 2026-10-13. Extended by ADR 212 decision 3. Decisions 2 and 3 superseded by ADR 227

## Context

ADR 46 makes built-in MFA cover console sign-in, the creation of access
tokens and the reveal of a secret value. It leaves open how long an MFA
sign-in stays valid for these two operations. On 2026-10-13 the project
owner answered: "Follow PCIDSS/SOC2/ISO guidance to pin defaults, and then
make it configurable."

The research note `docs/research/m4-identity-defaults.md` found no number for
the freshness of MFA before a sensitive operation in any primary source that
it read:

- PCI DSS v4.0.1 requirements 8.4.1 to 8.4.3 require MFA at each point of
  access, not for each operation.
- NIST SP 800-53 Rev. 5 IA-11 names privileged functions as a reason to
  authenticate again. It gives no time.
- ISO/IEC 27002:2022 and the SOC 2 Trust Services Criteria give no number in
  the parts that the note could read.

The nearest numbers are for an idle session. PCI DSS v4.0.1 requirement 8.2.8
requires a new authentication after a session is idle for more than 15
minutes. NIST SP 800-63B-4 recommends 15 minutes of idle time at AAL3.

## Decision

1. **A freshness window.** Token creation and secret reveal need an MFA step
   that is younger than the freshness window. When the last MFA step is
   older, the server asks for a new MFA step before it acts.
2. **15 minutes by default.** The default window is 15 minutes, for both
   operations. The number comes from PCI DSS v4.0.1 requirement 8.2.8, by
   analogy. No standard that the note read gives a number for this case.
3. **Two settings.** An administrator sets the window for token creation and
   the window for secret reveal apart. This follows ADR 46 decision 7, which
   keeps the MFA setting for a secret reveal separate.
4. **Configurable.** An administrator can set each window to another value.
   A window of zero asks for an MFA step for each operation. The server
   writes an audit entry for each change.
5. **Only where MFA applies.** The window applies when built-in MFA covers
   the operation (ADR 46). With MFA off, this ADR adds no step.
6. **API.** Each setting has a REST API and an action (ADR 17).

## Not decided here

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) keeps these open:

- How freshness works with an SSO identity provider, where MFA is the job of
  the provider (ADR 46). One way is to ask the provider for a new sign-in.
- The defaults for the idle timeout and the maximum length of a console
  session. The research note gives 15 minutes idle (PCI DSS 8.2.8) and 12
  hours at most (NIST SP 800-63B-4, AAL3).
- Whether the settings belong to the installation, to a namespace, or to
  both.

## Consequences

- The server records the time of the last MFA step of each session.
- A client that creates a token or reveals a secret must handle an answer
  that asks for a new MFA step. The console shows the MFA prompt and then
  repeats the request.
