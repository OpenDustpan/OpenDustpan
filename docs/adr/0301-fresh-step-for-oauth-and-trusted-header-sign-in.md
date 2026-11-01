# 301. A passkey or TOTP gives the fresh step to a person who signed in with OAuth or a trusted header

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026. The owner first proposed it in an earlier week, when the owner discussed GitHub as a sign-in provider

Changes:

- Changes ADR 46 decision 2: for two sign-in methods, OpenDustpan asks for its own factor at the fresh step.
- Extends ADR 55 decisions 6 and 7: the trusted header and a plain OAuth 2.0
  provider give no authentication time, so the fresh step uses a factor of
  OpenDustpan.
- Extends ADR 227 decision 1: a passkey or TOTP is a fresh authentication for these two methods.

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
   decision for local password users.
4. **No factor, no fresh step (owner).** A person of these methods who has
   enrolled no factor cannot do a step that needs a fresh authentication.
   The refusal is `403` with the error code `mfa.fresh-step-required`
   (ADR 227 decision 6). Its text names the enrollment page of passkeys and
   TOTP. The server does not skip the step. This is the fail-closed rule.
5. **One code path.** The passkey and TOTP code is the code of the local-user
   MFA. The enrollment, the check, the recovery codes and the audit entries
   are the same. The audit entry of a fresh step names the method of the
   sign-in and the factor that gave the step.

## Consequences

- An operator who uses GitHub as a sign-in provider, or a proxy header, can
  use all steps that need a fresh authentication. Each person enrolls one
  passkey or one TOTP device first.
- A person of these two methods who enrolls no factor still signs in, and
  can still do each action that needs no fresh step.
- The enrollment page must be reachable for a person of these methods. The
  enrollment itself needs no fresh step, because the person has no factor
  yet. Later specs M4-32 and M4-38 depend on this ADR.
- The code of local-user MFA (M4-29 WebAuthn and M4-31 TOTP) is shared. A
  change to it applies to these two methods too.
