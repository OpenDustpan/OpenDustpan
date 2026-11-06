# 57. Break-glass recovery when the last administrator loses every factor

Status: accepted · 2026-10-13 · decision 2 refined by ADR 245: the setting `--recovery-mode` holds the mode, and the host command refuses mode 3

Changed by:

- ADR 70 changes decision 4: the recovery token makes a temporary
  administrator.
- ADR 245 changes decision 2: the recovery-mode setting and the refusal of
  mode 3.

## Context

ADR 46 adds built-in MFA with recovery codes. It leaves open how a person
signs in after they lose every factor and every recovery code. An
administrator can reset the MFA of another user (ADR 46, decision 9). When
the last administrator loses every factor, no administrator remains to do
that reset.

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) gave three options:

1. The operator sets a new bootstrap admin token in the configuration or the
   environment and restarts. The token works once, the server audits it, and
   the operator must retire it.
2. A recovery request signed with an offline operator key.
3. No break-glass. The operator restores from a backup.

On 2026-10-13 the project owner answered: "Again, follow ISO/SOC/FIPS/PCIDSS
guidance if any precedent exists as a default. and then make it configurable
to on first-boot to support either 1, 2, or 3. Default to 1 if no guidance
exists from standards bodies."

The research note `docs/research/m4-identity-defaults.md` found that no
standard that it read names one of the three options as the preferred one.
Two sources set controls for emergency access:

- PCI DSS v4.0.1 requirement 8.2.2 accepts "break the glass" administrator
  access for when "all other authentication methods have failed". The use
  must be an exception, for no longer than needed, with a documented reason,
  management approval and a check of the identity of the person. Each action
  must be attributable to a person.
- NIST SP 800-53 Rev. 5 AC-2(2) asks that the system removes or disables
  emergency accounts automatically after a set time.

These controls fit option 1. FIPS 140-3 covers cryptographic modules and
does not apply to this question.

## Decision

1. **Three break-glass modes.** The server supports the three options of
   RFC 21: a one-time bootstrap admin token (mode 1), a recovery request
   signed with an offline operator key (mode 2), and no break-glass, with a
   restore from a backup (mode 3).
2. **The operator chooses the mode at first boot.** The operator sets the
   mode when the installation starts for the first time.
3. **Mode 1 is the default.** No standard names a preferred option, so the
   default of the owner applies. The controls of PCI DSS 8.2.2 and NIST SP
   800-53 AC-2(2) also fit mode 1.
4. **Mode 1 works once.** The operator sets a new bootstrap admin token in
   the configuration or the environment (ADR 6) and restarts the server. The
   token works for one recovery. Then the operator must retire it (ADR 53,
   decision 9).
5. **Every break-glass use writes an audit entry.** This applies to mode 1
   and mode 2. The entry holds the mode, the time and the source of the
   request.
6. **API.** The break-glass setting and each recovery step have a REST API
   and an action (ADR 17), except for the token that the server reads
   before it starts.

## Not decided here

[RFC 21](../rfc/0021-directory-sync-and-sign-in.md) keeps these open:

- Whether an operator can change the mode after the first boot, and how.
- What "works once" means for mode 1: one sign-in, one MFA reset, or a time
  limit. NIST SP 800-53 AC-2(2) asks for an automatic disable after a set
  time, and gives no number.
- How the audit entry of a break-glass use names a person, as PCI DSS 8.2.2
  asks.
- The format of the offline operator key and of the signed recovery request
  of mode 2.
- Whether "require MFA" (ADR 46) applies to the bootstrap admin token.

## Consequences

- The inactivity disable of ADR 53 skips the break-glass credential by
  default (ADR 53, decision 5).
- Mode 3 still needs the bootstrap admin token on the empty installation
  that receives the backup (ADR 39).
- The docs for operators describe each mode and the steps of a recovery.
- Mode 2 needs a signature check in the server and a tool for the operator
  that signs a request offline. A new library for this is a new dependency.
  Ask the project owner before adding it.
