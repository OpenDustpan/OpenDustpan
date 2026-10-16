# 68. Audit log retention, and the audit switch stays a start setting

Status: accepted · 2026-10-14

## Context

ADR 60 decision 21 leaves retention of the audit log open.
[RFC 22](../rfc/0022-audit-log.md) notes that PCI DSS v4.0.1 requirement
10.5.1 asks for 12 months of audit history, with the last three months at
hand.

ADR 60 decision 2 makes `--audit` the one switch that turns the audit log on
or off. [RFC 68](../rfc/0068-installation-settings.md) asks whether the API
can change it at run time. A person who turns the audit log off can hide the
next actions.

The project owner decided both on 2026-10-14:

- Retention: "Keep forever by default".
- The switch: it stays a start setting. "It also means that hosted's audit
  log can never be turned off in any case. A non issue in my opinion."

## Decision

1. **The server keeps audit entries forever by default.** A new
   installation deletes no audit entry.
2. **Retention is a setting.** An operator can set a retention period for
   the installation. The server then deletes entries that are older than the
   period. A change of the period writes an audit entry. The deletion keeps
   the chain check of ADR 60 decision 17 and ADR 64 valid. A later ADR
   decides how, for example with a retention anchor (RFC 22).
3. **The audit switch is a start setting only.** `--audit` and
   `DUSTPAN_AUDIT` set it when the server starts. No API, no reload of the
   configuration file (ADR 63) and no console control changes it while the
   server runs.
4. **The guides state the rules that apply to retention.** The audit log
   guide states:
   - PCI DSS v4.0.1 requirement 10.5.1 asks for at least 12 months.
   - Audit entries hold personal data, for example names, email addresses
     and IP addresses. Under the GDPR storage limitation principle, an
     operator in the scope of the GDPR sets a retention period that fits
     its purpose.

## Consequences

- The audit log grows without limit on a default installation. The sizing
  guide states the growth for each request.
- The hosted service runs with the audit log on, and no customer can turn
  it off. The hosted service sets its own retention period.
- The row "Audit log on or off" of RFC 68 is static. The row "Audit
  retention" is a run-time setting with no default period.
