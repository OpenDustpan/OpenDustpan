---
rfc: 21
title: Directory sync and sign-in hardening
status: discussing
created: 2026-10-10
---

# RFC 21. Directory sync and sign-in hardening

Status: discussing. Opened as a stub from the competitor review in the
research doc "IaC and TACOS landscape". The project owner chose
the built-in MFA below on 2026-10-11.

The part on built-in MFA is accepted as ADR 46. The rest stays open.

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

## Open questions

- Which of these belong in the binary, and which are better left to the
  operator's proxy?
- How does a user sign in when they lose every factor and every recovery
  code?
- Does "require MFA" apply to the bootstrap admin token?
- How long does an MFA sign-in stay valid for the creation of access tokens
  and for the reveal of a secret value?

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

## Outcome

The part on built-in MFA is accepted as ADR 46 on 2026-10-12. The rest has no
outcome yet.
