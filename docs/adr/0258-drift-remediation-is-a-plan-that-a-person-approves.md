# 258. Drift remediation is a plan that a person approves, by a built-in gate

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Accepts part of RFC 25 (the open question on automatic remediation). Uses ADR 226 decision 7. Decision 7 of ADR 226 extended by a third built-in gate of wider scope. Phase table of ADR 205 extended by a plan phase for a scheduled refresh job

## Context

Decided by the product manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

[RFC 25](../rfc/0025-drift-detection.md) proposes a scheduled run for each
unit that finds drift, and lists four actions on a drift: ignore, accept the
cloud (sync the state), revert the cloud (apply), or open a pull request
that changes the code. It asks: "Is automatic remediation allowed, and under
which edit policy?"

The gate model (ADRs 222 to 230) lets a change go on with no person when
each gate allows it: `autoApply` is `true` (ADR 229 decision 3). A schedule
is a source kind of the facts (`source.kind` `schedule`, ADR 226 decision
4). The `drift` fact group exists. So with no rule, a namespace with no
approval gate would apply each remediation that a schedule starts, with no
person.

A drift can come from an incident response, a manual fix during an outage
or an attack. A revert of such a change can make an outage worse or remove
evidence. A schedule cannot tell these cases apart. A person can.

## Decision

1. **Detection changes nothing.** A drift run is a job that only plans: a
   refresh-only plan for OpenTofu, and a preview with refresh for the
   Pulumi CLI. It has no apply phase. It sets the drift facts and the
   status of the unit.
2. **Remediation is a separate job.** Each action that changes the state or
   the resources (accept the cloud, or revert the cloud) is its own job,
   with its own plan phase and its own stored plan. A drift run never
   becomes an apply.
3. **A person approves each remediation that a schedule starts, by a
   built-in gate.** The built-in gate
   `builtin/schedule-apply-needs-approval` (ADR 226 decision 7) makes the
   apply phase of a job with `source.kind` `schedule` wait for the
   approval of a person on the stored plan of that job (ADR 191). The scope
   of this gate is each job with `source.kind` `schedule` whose operation
   is `apply`, `destroy` or `refresh`. A remediation job of the operation
   `refresh` has a plan phase (OpenTofu `plan -refresh-only`, Pulumi
   `refresh --preview-only`) and a stored plan, so the gate holds before
   its write phase. The gate is `enforce` by default. An installation
   administrator can switch it to `evaluate`, with fresh authentication and
   an audit entry. An access limit can lock that switch, except for a
   break-glass session (ADR 223 decision 7). Only the installation scope
   can set that limit: the installation owner sets it, not each team. The
   principal of the
   schedule cannot approve. `autoApply` is `false` for such a job while
   the gate enforces, and the decision record names the gate.
4. **A second escape hatch is the API.** A team that wants automatic remediation
   starts the remediation job through the API (ADR 17) from its own
   automation, with a principal of its own. That job has the source kind
   of its caller, meets the normal gates and approval rules, and the audit
   log names the principal. The team owns that decision and its record.
5. **Ignore is a record.** An "ignore" of a drift is a record with a
   person, a reason and an expiry. It changes no resource. RFC 25 names
   the action that it needs and its audit entry.
6. **Open in RFC 25.** The action and the audit entry of an ignore, the schedule, its spread and its pause, the status
   vocabulary (RFC 26), the pull request action (RFC 12), and the fixed
   steps and the clean environment of a drift run (RFC 95 section D) stay
   open.

Benchmark: AWS Config runs automatic remediation only when the owner
configures it for each rule. Hosted IaC services detect drift and notify by
default, and an apply needs a person or an opt-in setting of the owner.
The gate has a safe default and an audited opt-out. SOC
2 change management and ISO 27001 change control ask for an authorized,
recorded approval of each change to production. DORA asks the financial
entity to manage ICT changes in a controlled way.

## Consequences

- A team sees a drift and a ready plan for each remediation. One approval
  applies it.
- A small team that wants automatic remediation writes it on the API, and
  its own principal shows in the audit log.
- A namespace cannot switch the gate off alone. Only an installation
  administrator can, with fresh authentication and an audit entry, and
  an installation access limit can lock that switch, except for a
  break-glass session. The user documentation of drift says this.
- This ADR extends two earlier decisions. ADR 226 decision 7 gets a third
  built-in gate, and the gate has a wider scope than the two gates that
  decision names. The phase table of ADR 100 decision 4 gets a plan phase
  for a scheduled job of the operation `refresh`.
