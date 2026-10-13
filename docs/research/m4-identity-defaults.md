# Research: identity defaults in security standards

Question: What default numbers and behaviors do the primary security
standards require or recommend for three identity settings? The settings
are the inactivity disable of accounts and access tokens (RFC 20), MFA
freshness and session timeouts (RFC 21), and break-glass access (RFC 21).

Date: 2026-10-13

This note gives facts. It does not choose a default. RFC 20 and RFC 21 hold
the proposals, and the project owner decides. It is not legal or audit
advice.

## 1. Summary

| Question | Strictest number in a primary source | Source |
| --- | --- | --- |
| Q1. Disable an inactive user account | 90 days | PCI DSS v4.0.1, 8.2.6 |
| Q1. Disable an inactive access token or machine credential | No number found | None |
| Q2. Idle session timeout | 15 minutes | PCI DSS v4.0.1, 8.2.8 (SHALL-level). NIST SP 800-63B-4, AAL3 (SHOULD) |
| Q2. Maximum session length before reauthentication | 12 hours | NIST SP 800-63B-4, AAL3 (SHALL) |
| Q2. MFA freshness before a sensitive operation | No number found | None |
| Q3. Break-glass | Allowed as an exception, with controls | PCI DSS v4.0.1, 8.2.2. NIST SP 800-53 Rev. 5, AC-2 and AC-2(2) |

- ISO/IEC 27002:2022 and the SOC 2 Trust Services Criteria give no numbers
  in the parts we could check. They leave each value to the organization.
- NIST SP 800-53 Rev. 5 gives no numbers. Each value is an
  organization-defined parameter.
- PCI DSS v4.0.1 and NIST SP 800-63B-4 are the only sources that set
  numbers.

## 2. Q1. Inactivity disable of accounts and access tokens

| Standard | Requirement | Exact quote (short) | Number |
| --- | --- | --- | --- |
| PCI DSS v4.0.1 | 8.2.6 | "Inactive user accounts are removed or disabled within 90 days of inactivity." | 90 days |
| PCI DSS v4.0.1 | 8.2.6, Good Practice | "the account should be disabled as soon as the leave begins, rather than waiting 90 days." | None (earlier, when absence is known) |
| PCI DSS v4.0.1 | 8.6.1 (system and application accounts) | "Interactive use is prevented unless needed for an exceptional circumstance." | None |
| PCI DSS v4.0.1 | 8.6.3 (system and application accounts) | Passwords "are changed periodically (at the frequency defined in the entity's targeted risk analysis ...)" | None (entity sets it) |
| PCI DSS v4.0.1 | 8.3.9 (single-factor passwords only) | "Passwords/passphrases are changed at least once every 90 days" or posture is "dynamically analyzed" | 90 days (rotation, not inactivity) |
| NIST SP 800-53 Rev. 5 | AC-2(3) Disable Accounts | "Disable accounts within [time period] when the accounts: ... (d) Have been inactive for [time period]." | None (organization-defined) |
| NIST SP 800-53 Rev. 5 | IA-5 f | "Changing or refreshing authenticators [time period by authenticator type] or when [events] occur" | None (organization-defined) |
| NIST SP 800-63B-4 | Sec. 4 (Authenticator events), "Expiration" | "CSPs MAY bind authenticators that expire to subscriber accounts." | None |
| ISO/IEC 27002:2022 | 5.16 Identity management, 5.18 Access rights | Text not read (paywalled) | Not verified |
| SOC 2 TSC (AICPA 2017, points of focus 2022) | CC6.2 | "user system credentials are removed when user access is no longer authorized." | None |

Notes:

- PCI DSS 8.2.6 names "user accounts". It has no Applicability Note for
  system or application accounts.
- PCI DSS 8.6.1 to 8.6.3 cover system and application accounts. They set
  no inactivity period. 8.6.3 leaves the change frequency to the entity's
  targeted risk analysis (Requirement 12.3.1).
- NIST SP 800-63B-4 has rules for authenticator expiration and
  invalidation. We found no rule that disables an account or an
  authenticator after a period of no use.
- NIST SP 800-53 Rev. 5 AC-2 guidance says some "accounts of last resort"
  "remain available and are not subject to automatic disabling or removal
  dates." This is relevant to Q3.

Default that follows:

- User accounts: 90 days. PCI DSS v4.0.1 Requirement 8.2.6 sets this
  number. It is the only number in the primary sources we read.
- Access tokens and machine credentials: no primary source sets an
  inactivity number. A 90-day default would match the PCI DSS user-account
  number. That is a choice by analogy, not a requirement. PCI DSS 8.6.3
  and NIST SP 800-53 IA-5 f leave the period to the organization.

## 3. Q2. MFA freshness, session timeouts and reauthentication

NIST SP 800-63B-4 is final. CSRC lists "SP 800-63B-4 (Final)" with the date
07/31/25 and "Date Published: July 2025". It supersedes SP 800-63B
(2017, updated 03/02/2020).

| Standard | Requirement | Exact quote (short) | Number |
| --- | --- | --- | --- |
| PCI DSS v4.0.1 | 8.2.8 | "If a user session has been idle for more than 15 minutes, the user is required to re-authenticate" | 15 minutes idle |
| PCI DSS v4.0.1 | 8.4.1, 8.4.2, 8.4.3 | "MFA is implemented for all non-console access into the CDE." | None (MFA at each access point, no freshness) |
| PCI DSS v4.0.1 | 8.4.2, Guidance | "if MFA is used when a user connects to the CDE network, it does not have to be used when the user logs into each system" | None |
| PCI DSS v4.0.1 | 8.5.1 | MFA "cannot be bypassed by any users, including administrative users unless ... authorized by management on an exception basis, for a limited time period." | None |
| PCI DSS v4.0.1 | 8.3.4 | Lock out "after not more than 10 attempts", lockout "a minimum of 30 minutes or until the user's identity is confirmed." | 10 attempts, 30 minutes |
| NIST SP 800-63B-4 | Sec. 2.1.3 (AAL1) | "SHOULD be no more than 30 days at AAL1. An inactivity timeout MAY be applied" | 30 days overall |
| NIST SP 800-63B-4 | Sec. 2.2.3 (AAL2) | "SHOULD be no more than 24 hours at AAL2. The inactivity timeout SHOULD be no more than 1 hour." | 24 hours overall, 1 hour idle |
| NIST SP 800-63B-4 | Sec. 2.3.3 (AAL3) | "the overall timeout for reauthentication SHALL be no more than 12 hours. The inactivity timeout SHOULD be no more than 15 minutes." | 12 hours overall, 15 minutes idle |
| NIST SP 800-63B-4 | Sec. 5.2 | "When either timeout expires, the session SHALL be terminated." | None |
| NIST SP 800-63B-4 | Sec. 4.1.2 | Binding a new authenticator "requires authentication at either the maximum AAL currently available ... or the maximum AAL at which the new authenticator will be used, whichever is lower." | None |
| NIST SP 800-53 Rev. 5 | AC-11 Device Lock | "initiating a device lock after [time period] of inactivity" | None (organization-defined) |
| NIST SP 800-53 Rev. 5 | AC-12 Session Termination | "Automatically terminate a user session after [conditions or trigger events]." | None (organization-defined) |
| NIST SP 800-53 Rev. 5 | IA-11 Re-authentication | "Require users to re-authenticate when [circumstances or situations]." Guidance lists "when the execution of privileged functions occurs". | None |
| NIST SP 800-53 Rev. 5 | IA-2(1) | "Implement multi-factor authentication for access to privileged accounts." | None |
| ISO/IEC 27002:2022 | 8.5 Secure authentication | Text not read (paywalled) | Not verified |
| SOC 2 TSC | CC6.1 | Criterion text not read in the AICPA PDF (see section 5) | None found |

Notes:

- The section numbers 2.1.3 and 2.2.3 appear as cross-references in the
  NIST text. The number 2.3.3 follows the same structure. The HTML page
  does not print the heading numbers, so 2.3.3 is an inference.
- NIST SP 800-63B-4 changed the AAL2 values. SP 800-63B (2017) had 12 hours
  overall and 30 minutes idle at AAL2. SP 800-63B-4 has 24 hours and 1 hour.
  The 12-hour value now applies at AAL3 only.
- At AAL2, after an idle timeout but before the overall timeout, the
  verifier "MAY allow the subscriber to reauthenticate using only a
  successful password or biometric comparison in conjunction with the
  session secret" (Sec. 2.2.3).
- At AAL3, "reauthentication requirements are the same as for initial
  authentication at AAL3" (Sec. 2.3.3).
- No source sets an MFA freshness window for a sensitive operation, such as
  creating an access token or revealing a secret value. NIST SP 800-53
  IA-11 names privileged functions as one situation for reauthentication.
  It gives no time. NIST SP 800-63B-4 Sec. 4.1.2 requires authentication at
  the AAL of the new authenticator when an authenticator is bound.

Conflicts:

- Idle timeout: PCI DSS 8.2.8 requires 15 minutes. NIST SP 800-63B-4
  recommends 1 hour at AAL2 and 15 minutes at AAL3. PCI DSS is the
  stricter requirement for a deployment that holds cardholder data.
- Overall session length: PCI DSS sets no number. NIST SP 800-63B-4 sets
  24 hours (SHOULD, AAL2) and 12 hours (SHALL, AAL3).

Default that follows:

- Idle timeout: 15 minutes. PCI DSS v4.0.1 8.2.8 requires it. NIST SP
  800-63B-4 Sec. 2.3.3 recommends it at AAL3.
- Maximum session length: 12 hours. NIST SP 800-63B-4 Sec. 2.3.3 requires
  it at AAL3. At AAL2 the recommended limit is 24 hours.
- MFA freshness for sensitive operations: no primary source gives a number.
  If the session is the unit, the idle and overall limits above bound it.
  A shorter window for token creation or secret reveal is a choice, not a
  requirement. NIST SP 800-53 IA-11 supports a reauthentication step for
  privileged functions.

## 4. Q3. Break-glass and emergency access

| Standard | Requirement | Exact quote (short) | Number |
| --- | --- | --- | --- |
| PCI DSS v4.0.1 | 8.2.2 | Shared IDs "are only used when necessary on an exception basis". "Use is limited to the time needed for the exceptional circumstance." | None |
| PCI DSS v4.0.1 | 8.2.2, Guidance | "An example of an exceptional circumstance is where all other authentication methods have failed, and a shared ID is needed for emergency use or "break the glass" administrator access." | None |
| PCI DSS v4.0.1 | 8.2.2 | "Use is explicitly approved by management." "Individual user identity is confirmed before access to an account is granted." "Every action taken is attributable to an individual user." | None |
| PCI DSS v4.0.1 | 8.5.1 | MFA bypass only if "authorized by management on an exception basis, for a limited time period." | None |
| NIST SP 800-53 Rev. 5 | AC-2 guidance | "Organizations establish emergency accounts in response to crisis situations and with the need for rapid account activation." | None |
| NIST SP 800-53 Rev. 5 | AC-2 guidance | "emergency account activation may bypass normal account authorization processes." | None |
| NIST SP 800-53 Rev. 5 | AC-2 guidance | "accounts of last resort ... remain available and are not subject to automatic disabling or removal dates." | None |
| NIST SP 800-53 Rev. 5 | AC-2(2) | "Automatically [remove or disable] temporary and emergency accounts after [time period]." | None (organization-defined) |
| NIST SP 800-53 Rev. 5 | AC-6(9) | "Log the execution of privileged functions." | None |
| NIST SP 800-53 Rev. 5 | IA-5 e | "Changing default authenticators prior to first use" | None |
| NIST SP 800-53 Rev. 5 | IA-5(1)(e) | "Require immediate selection of a new password upon account recovery" | None |
| NIST SP 800-63B-4 | Sec. 4.2 | CSPs "SHALL support one or more of these" recovery methods (saved codes, issued codes, recovery contacts, repeated identity proofing). | None |
| NIST SP 800-63B-4 | Sec. 4.2.1.1 | Saved recovery codes "are intended to be maintained offline". After use the CSP "SHALL invalidate that recovery code and SHALL issue a new saved recovery code". | At least 64 bits |
| NIST SP 800-63B-4 | Sec. 4.2.1.2 | Issued codes valid "at most" 10 minutes (text or voice), 24 hours (email), 21 or 30 days (postal). | 10 minutes to 30 days |
| NIST SP 800-63B-4 | Sec. 4.2, "Recovery at AAL2" | Requires "Two recovery codes obtained using different methods", or one code plus a bound single-factor authenticator, or repeated identity proofing. | 2 factors |
| NIST SP 800-63B-4 | Sec. 4.2, "Account Recovery Notification" | "account recovery SHALL cause a notification to be sent to the subscriber or their designee" | None |
| ISO/IEC 27002:2022 | 8.2 Privileged access rights | Text not read (paywalled) | Not verified |
| SOC 2 TSC | CC6.1 to CC6.3 | No break-glass text found | None |

How the three options under study compare with the sources:

1. New bootstrap admin token in config or env, then restart. It works once,
   it is audited, and it must be retired. This is an emergency account in
   the sense of NIST SP 800-53 AC-2. AC-2(2) asks to remove or disable it
   automatically after a set time. PCI DSS 8.2.2 asks for limited time, a
   documented reason, management approval, a confirmed individual identity
   and attributable actions. Both sources accept this type of access when
   these controls are in place.
2. A recovery request signed with an offline operator key. This is close to
   a saved recovery code in NIST SP 800-63B-4 Sec. 4.2.1.1, which is kept
   offline and replaced after one use. But Sec. 4.2 is about a subscriber
   who recovers their own account at a CSP. It is not about an operator who
   restores the last administrator of an installation. The match is by
   analogy only.
3. No break-glass, restore from a backup. No source we read requires a
   break-glass path. No source we read names backup restore as the
   recovery path for lost administrator credentials.

Which option a standard favors: no standard we read names one of the
three options. PCI DSS 8.2.2 and NIST SP 800-53 AC-2 describe the controls
that option 1 needs. NIST SP 800-63B-4 Sec. 4.2 describes controls that
are closest to option 2. No source asks for dual control on emergency
access in the text we read.

Default that follows: no primary source sets a number for break-glass.
The controls that the sources state are: exception use only, limited time,
automatic disable after use (AC-2(2)), a documented reason, approval,
a confirmed individual identity, a log of each action, and a notification
after recovery (SP 800-63B-4 Sec. 4.2).

## 5. What the sources do not say

- No source sets an inactivity period for access tokens, API keys or
  machine credentials. PCI DSS 8.2.6 names user accounts only.
- No source sets an MFA freshness window for a single sensitive operation.
- No source names one of the three break-glass options as preferred.
- No source asks for dual control or an offline key for emergency access in
  the text we read.
- NIST SP 800-53 Rev. 5 leaves every number to the organization. Its
  baselines (SP 800-53B) select controls but do not fill in the values.
  We did not read FedRAMP or other overlays that fill in values.
- ISO/IEC 27002:2022: ISO's pages returned HTTP 403 to our tools. We could
  not read the control text or confirm the control titles on an ISO page.
  The titles in this note (5.15 Access control, 5.16 Identity management,
  5.17 Authentication information, 5.18 Access rights, 8.2 Privileged
  access rights, 8.5 Secure authentication) are as commonly cited. ISO
  27002 is a guidance standard, and we expect it to give no numbers. We
  could not verify this.
- SOC 2 Trust Services Criteria: the AICPA PDF needs a free account
  sign-in, and we did not read it. The CC6.2 wording in this note comes
  from secondary sources that reproduce it. AICPA states that the 2022
  revision changed only the points of focus, not the criteria. We found no
  number in the CC6.1 to CC6.3 criteria text. We could not check the
  points of focus.
- FIPS 140-3 covers cryptographic modules. It is not relevant to these
  numbers, and we did not read it for this note.

## 6. Method and limits

- NIST SP 800-63B-4: we read the HTML edition on pages.nist.gov and the
  CSRC status page.
- NIST SP 800-53 Rev. 5: we read the official OSCAL JSON catalog, release
  5.2.0, from NIST's `usnistgov/oscal-content` repository. Bracketed text is
  the label of an organization-defined parameter.
- PCI DSS v4.0.1: PCI SSC serves the document only after a license
  click-through, and a direct download returned HTTP 403. We read a copy
  of the PCI SSC PDF hosted by Middlebury College. Its cover says "Version
  4.0.1, June 2024", and its PDF author field is
  `PCISSC@pcisecuritystandards.org`. SHA-256:
  `5e6b9093b84007b973097d20126a3768ea2f0a1d4200255c849b0fb3bf04ebc7`.
  Compare it with a copy from the PCI SSC document library before you rely
  on it for an audit.

## 7. Sources

All accessed 2026-10-13.

- NIST SP 800-63B-4, Digital Identity Guidelines: Authentication and
  Authenticator Management (HTML):
  https://pages.nist.gov/800-63-4/sp800-63b.html
- NIST SP 800-63B-4, CSRC publication page (final, July 2025):
  https://csrc.nist.gov/pubs/sp/800/63/b/4/final
- NIST SP 800-53 Rev. 5, OSCAL catalog, release 5.2.0:
  https://raw.githubusercontent.com/usnistgov/oscal-content/main/nist.gov/SP800-53/rev5/json/NIST_SP-800-53_rev5_catalog.json
- PCI DSS v4.0.1, Requirements and Testing Procedures, June 2024, PCI SSC
  document library (license click-through):
  https://docs-prv.pcisecuritystandards.org/PCI%20DSS/Standard/PCI-DSS-v4_0_1.pdf
- The copy we read:
  https://www.middlebury.edu/sites/default/files/2025-01/PCI-DSS-v4_0_1.pdf
- AICPA, 2017 Trust Services Criteria (With Revised Points of Focus, 2022),
  download page (free account needed, not read):
  https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022
- ISO/IEC 27002:2022 catalog page (HTTP 403 to our tools, not read):
  https://www.iso.org/standard/75652.html
