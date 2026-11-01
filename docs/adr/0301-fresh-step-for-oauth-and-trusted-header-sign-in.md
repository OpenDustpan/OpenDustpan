# 301. A passkey or TOTP gives the fresh step to a person who signed in with OAuth or a trusted header

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026. The owner first proposed it in an earlier week, when the owner discussed GitHub as a sign-in provider

Changes:

- Changes ADR 46 decision 2: for two sign-in methods, OpenDustpan asks for its own factor at the fresh step.
- Extends ADR 55 decisions 6 and 7: the trusted header and a plain OAuth 2.0
  provider give no authentication time, so the fresh step uses a factor of
  OpenDustpan.
- Extends ADR 55 decision 10: the rule "MFA is the job of the provider" has a narrow exception for the fresh step.
- Extends ADR 56 decision 5: the rule applies when MFA is on, as for every other method.
- Extends ADR 227 decision 1: a passkey or TOTP is a fresh authentication for these two methods.
- Extends ADR 285 decision 1: the same narrowing of ADR 46 decision 2 for two more methods.

## Context

ADR 227 decision 1 says that a fresh authentication is a built-in MFA step
(ADR 46) or a new authentication at the SSO identity provider (ADR 71). The
steps that need it are, for example, token creation, a secret reveal, a
weaker setting and a destroy approval (ADR 56, ADR 227 decision 2).

Two sign-in methods give no authentication time:

- A plain OAuth 2.0 provider, for example GitHub as a sign-in provider (ADR
  55 decision 7). The answer of the user-info endpoint has no time.
- The trusted header of a proxy (ADR 55 decision 6). The header names the
  person. It does not say when the person authenticated.

OIDC (`max_age`), SAML (`ForceAuthn`) and CAS (`renew=true`) can ask the
provider for a new authentication. These two methods cannot. ADR 46
decision 2 says that with an SSO identity provider, OpenDustpan adds no MFA
step of its own. So today a person of these two methods has no way to reach
a fresh step.

Benchmark: GitHub sudo mode accepts a passkey, a security key or TOTP for a
sensitive action, whatever the sign-in was. Google IAP headers carry no
authentication time.

## Decision

Rules 1 to 5 are from the first review round. Rules 6 to 13 record the
decisions of the project manager (PM) for the open points of that review,
under the owner's delegation.

1. **A factor of OpenDustpan gives the fresh step (owner).** For a person
   who signed in with a plain OAuth 2.0 provider or with the trusted header
   of a proxy, the fresh authentication of ADR 227 decision 1 is a passkey
   or a TOTP code of that person. The person enrolled the factor with
   OpenDustpan. The window and the other rules of ADR 227 stay the same.
2. **Only these two methods (owner).** OIDC keeps `max_age`, SAML keeps
   `ForceAuthn`, and CAS keeps `renew=true`. This ADR changes nothing for
   them.
3. **Sign-in stays the same (owner).** The sign-in with these two methods
   asks for no second factor. So ADR 46 decision 2 stays true for sign-in.
   This ADR narrows it: for these two methods, OpenDustpan asks for its own
   factor at the fresh step only. ADR 285 decision 1 narrowed the same
   decision for local password users. ADR 55 decision 10 keeps its meaning
   for sign-in.
4. **No factor, no fresh step (owner).** A person of these methods who has
   enrolled no factor cannot do a step that needs a fresh authentication.
   The refusal is `403` with the error code `mfa.fresh-step-required`
   (ADR 227 decision 6). Its text names the enrollment page of passkeys and
   TOTP. The server does not skip the step. This is the fail-closed rule.
5. **One code path.** The passkey and TOTP code is the code of the local-user
   MFA. The enrollment, the check, the recovery codes and the audit entries
   are the same.
6. **First enrollment needs an enrollment code (PM).** A person of these
   methods who has no factor enrolls the first one with an enrollment code
   that an administrator issues. The code is short-lived, works one time,
   and is bound to that person. Each issue and each use writes an audit
   entry. An administrator can issue codes for many people in one request.
   So a stolen session alone cannot enroll a factor. Benchmark: Microsoft
   Entra Temporary Access Pass. GitHub asks for sudo mode to add a factor.
7. **An administrator reset returns the person to "no factor" (PM).** After
   a reset of the MFA of a person, the next enrollment needs a new
   enrollment code again.
8. **A change of a factor needs a fresh step (PM).** When a person has a
   factor, adding or removing a factor needs a fresh step with a factor
   that exists.
9. **A recovery code gives no fresh step (PM).** A recovery code lets the
   person enroll one replacement factor, one time, and the server writes an
   audit entry. It never passes a fresh step. Benchmark: GitHub sudo mode
   does not accept recovery codes.
10. **Each change of a factor tells the person (PM).** An enrollment, a
    removal and a reset each write an audit entry and send a notice to the
    notification channel of the person. Each one also ends the other
    sessions of the person. The session that did the change stays.
11. **The step belongs to one session (PM).** The server records the fresh
    step on the session that did it, and on no other session. It lasts for
    `freshAuthSeconds` or until that session ends. The audit entry of the
    step names the session. The session model is the model of ADR 297.
12. **Only when MFA is on (PM).** ADR 56 decision 5 holds. On an
    installation with MFA off, the server asks for no fresh step, as for
    every other method. The rules of this ADR apply when MFA is on.
13. **The refusal names the factor step (PM).** For a person who has a
    factor, the `403` of decision 4 names the factor step. It does not name
    a new sign-in. For a person with no factor, it names the enrollment page
    (decision 4) and the enrollment code (decision 6).

## Consequences

- An operator who uses GitHub as a sign-in provider, or a proxy header, can
  use all steps that need a fresh authentication. Each person gets an
  enrollment code from an administrator and enrolls one passkey or one TOTP
  device first.
- A person of these two methods who enrolls no factor still signs in, and
  can still do each action that needs no fresh step.
- The enrollment page must be reachable for a person of these methods. The
  first enrollment needs the enrollment code, not a fresh step, because the
  person has no factor yet. Later specs M4-32 and M4-38 depend on this ADR.
- The code of local-user MFA (M4-29 WebAuthn and M4-31 TOTP) is shared. A
  change to it applies to these two methods too.
- The enrollment code is a new object with a REST API and an action
  (ADR 17). The spec of the code covers its length, its life time and its
  limit.
