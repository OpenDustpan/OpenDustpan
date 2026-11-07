# 347. Warn, do not block, for choices that the operator owns

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 200 decision 10: the personas stay equal, and a risky choice of the operator gets a warning, not a block.

Changed by:

- ADR 351 extends decision 4: the integrity of the served bytes is a security control. The meaning of provenance is not.

## Context

ADR 200 decision 10 asks for worked examples for a homelab user, a small team and a platform team. It says that all personas are equal. RFC 9 asks if "warn, do not block" becomes a project-wide principle. A block that protects a regulated enterprise stops a homelab user from a choice that is theirs. A warning alone does not fit a regulated enterprise.

On 7 November 2026 the project owner decided the rule.

## Decision

Decided by the project owner on 7 November 2026.

1. **Warn, do not block (owner, 2026-11-07).** When the operator of an installation makes a risky choice about something that the operator owns, the server warns. It does not block the choice. The warning names the risk and the setting to change.
2. **The personas (owner, 2026-11-07).** The rule in decision 1 is the default for the homelab, startup and platform personas.
3. **A strict setting (owner, 2026-11-07).** An installation-wide strict setting turns the warnings of decision 1 into blocks. It is for the regulated enterprise. The API shows the effective value of the setting (see "No magic" in `CLAUDE.md`).
4. **Security controls keep their own rules (owner, 2026-11-07).** This ADR does not change a security control. Authentication, authorization, secrets and the audit log keep the rules of their own ADRs, in every mode.

## Consequences

- This ADR does not decide the name of the strict setting, its place in the installation settings, or the list of choices that warn. A later ADR or RFC covers them.
- This ADR does not decide which choices count as "operator-owned" and which are security controls. A feature that adds a warning states that in its own ADR.
- A change of the strict setting is a sensitive setting, so it probably needs an audit entry. This ADR does not decide it.
- RFC 9 asks this question. A later change to RFC 9 can link to this ADR.
