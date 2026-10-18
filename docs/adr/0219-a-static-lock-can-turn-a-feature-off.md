# 219. A lock in the static layer can turn a feature off (owner)

Status: accepted · 2026-10-18 · answers RFC 68 open question 11

## Context

RFC 68 ("No setting gates a feature") proposes this rule: the static layer
of a hosted installation can lock a limit or a default, but "must not lock a
feature off for the customer, for example the audit log, MFA or SSO". Its
open question 11 asks whether the server enforces that rule with a list of
switches that a lock can set only to on.

ADR 63 gives the static layer: flags, environment variables and one YAML
file. A value from the static layer is locked, and the API refuses to change
it. ADR 211 (in review) gives each optional module one declared switch, and
an off module answers 404 with the error code `module.off`.

ADR 59 decision 1 says that the hosted service "never turns a feature off
for its customers". ADR 70 decision 23 says that the hosted service cannot
turn break-glass off.

The project owner decided on 2026-10-18, as the project manager relayed: a
lock in the static layer may turn a feature off.

## Decision

1. **A lock can set a switch to off (owner).** The static layer can lock
   each declared switch to on or to off, also a security switch. The server
   keeps no list of switches that a lock can set only to on. This answers
   RFC 68 open question 11.
2. **Lower layers follow the lock.** When the static layer locks a switch
   off, no account and no namespace can turn it on. An account or a
   namespace can still turn off a switch that the installation leaves on,
   as ADR 44 decision 6 narrows a switch.
3. **A lock is visible.** The settings resource of each scope (ADR 212
   decision 7, in review) answers the switch with `locked: true` and `source: static`.
   The capabilities document (ADR 211) answers an off module as off. A
   refused request names the switch and says that the operator locked it.
   The console shows the switch as locked, with no control to change it.
4. **A security switch that a lock turns off is reported.** A security
   switch is one of: the audit log, the hash chain, signed checkpoints, the
   MFA requirement, break-glass, revoke on removal, and the IP allowlist
   feature. When a lock turns one off:
   - each start writes a WARN log line that names the switch, and an audit
     entry in the `@server` scope (ADR 63 decision 10).
   - the admin access report (ADR 70 decision 29) lists it.
   - on an installation with more than one account, the audit scope of each
     account gets one entry when the lock first applies, and the account
     settings show it. So each tenant can see what the operator turned off.
5. **The no-gate check still holds.** A lock is a value of an ordinary
   setting. It exists in every build, and it reads no license, edition,
   plan or "hosted" flag. The name test of RFC 68 (`edition`, `plan`,
   `hosted`, `license`, `tier`) stays.
6. **OpenDustpan Cloud.** This ADR decides what the server can do. It does
   not change the rules of the hosted service: ADR 59 decision 1 and ADR 70
   decision 23 still say what OpenDustpan Cloud turns off for its tenants.
   A change of those rules needs the owner (RFC 76).

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- ISO/IEC 27001:2022 Annex A 8.9 (configuration management): decisions 3
  and 4.
- SOC 2 CC8.1 (changes to infrastructure are authorized and recorded):
  decision 4.

## Consequences

- `pkg/settings` drops any plan for an "only on" list. Its tests check that
  a lock to off applies at each lower layer, and that the settings answer
  shows `locked`.
- The admin access report and the start audit entry name each security
  switch that a lock turns off.
- RFC 68 gets an outcome line for open question 11.
