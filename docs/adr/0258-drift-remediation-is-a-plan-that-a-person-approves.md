# 258. Drift remediation is a plan that a person approves, never an automatic apply

Status: accepted · 2026-10-20. Decided by the project manager under the owner's delegation of 20 October 2026. Accepts part of RFC 25 (the open question on automatic remediation). Adds a fixed control to ADR 222 decision 4

## Context

Decided by the project manager under the owner's delegation of 20 October
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
3. **A person approves each remediation that a schedule starts.** A fixed
   control (ADR 222 decision 4): the apply phase of a job with
   `source.kind` `schedule` waits for the approval of a person on the
   stored plan of that job (ADR 191). No gate, no approval default and no
   namespace setting removes this wait. The principal of the schedule
   cannot approve. `autoApply` is `false` for such a job, and the decision
   record shows the fixed control.
4. **The escape hatch is the API.** A team that wants automatic remediation
   starts the remediation job through the API (ADR 17) from its own
   automation, with a principal of its own. That job has the source kind
   of its caller, meets the normal gates and approval rules, and the audit
   log names the principal. The team owns that decision and its record.
5. **Ignore is a record.** An "ignore" of a drift is a record with a
   person, a reason and an expiry. It changes no resource.
6. **Open in RFC 25.** The schedule, its spread and its pause, the status
   vocabulary (RFC 26), the pull request action (RFC 12), and the fixed
   steps and the clean environment of a drift run (RFC 95 section D) stay
   open.

Benchmark: AWS Config runs automatic remediation only when the owner
configures it for each rule. Hosted IaC services detect drift and notify by
default, and an apply needs a person or an opt-in setting of the owner. SOC
2 change management and ISO 27001 change control ask for an authorized,
recorded approval of each change to production. DORA asks the financial
entity to manage ICT changes in a controlled way.

## Consequences

- A team sees a drift and a ready plan for each remediation. One approval
  applies it.
- A small team that wants automatic remediation writes it on the API, and
  its own principal shows in the audit log.
- A namespace cannot configure the wait away. The user documentation of
  drift says this.
