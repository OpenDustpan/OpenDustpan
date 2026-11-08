# 358. Automatic drift remediation is opt-in, and a namespace can always opt out

Status: accepted · 2026-11-07. Records the owner decision of 21 October 2026 on automatic drift remediation

Changes:

- Extends ADR 263 decision 7: automatic remediation (auto-dedrift) is available, and it is opt-in. ADR 263 decision 1 stays true for the drift check itself.

## Context

[ADR 263](0263-drift-detection-is-a-plan-only-refresh-with-an-opt-in-schedule.md)
decision 7 puts automatic remediation out of scope and sends the reader to
[ADR 258](0258-drift-remediation-is-a-plan-that-a-person-approves.md). ADR 258
decides that a remediation is a separate job, that a built-in gate makes a
person approve a remediation that a schedule starts, and that a team which
wants automatic remediation can start the job through the API with its own
principal. It does not say whether the server itself can start a remediation
job when a drift check finds a drift.

The owner decided on 21 October 2026 that automatic remediation of drift is
available, but opt-in. Auto-dedrift means that the server starts a job that
brings a drifted unit back to its code. It is off by default. Like every
optional feature, a namespace can also turn it off.

## Decision

1. **Auto-dedrift is available and opt-in (owner, 2026-10-21).** The server
   has the feature. It is off by default. With no setting that turns it on,
   a drift result starts no remediation job. A drift check still only plans
   (ADR 263 decision 1).
2. **An auto-dedrift job is a separate job that a drift result starts.** It
   is not the drift check and it does not turn the drift check into an
   apply. The drift check keeps `Changes()` false and its plan phase only.
   The remediation job is a job of its own with its own plan phase and its
   own stored plan, as ADR 258 decision 2 says.
3. **A setting turns the feature on.** The setting has a value at an object
   that can give a default. The same objects that give a default for the
   drift schedule (ADR 263 decision 5) are the candidates. Until a principal
   with the settings action of that object turns the setting on, the feature
   is off for that object.
4. **A namespace can turn the feature off, and its opt-out always wins.**
   When a namespace has auto-dedrift off, no opt-in above it or below
   it starts a remediation job in that namespace. An opt-in at the
   installation, at a collection or at a unit does not override the opt-out
   of the namespace. To use the feature again, a principal with the settings
   action of the namespace removes the opt-out.
5. **The API shows the effective value.** For each unit, the API shows if
   auto-dedrift is on or off, and which setting decided it: the object, the
   value and the principal and time that set it. This follows the rule that
   each default shows its effective value. A user can ask why a unit was
   remediated or was not, and the answer names the setting.
6. **An auto-dedrift job goes through the same controls as a job that a
   person starts.** It is a job that changes state, so `Changes()` is true.
   It meets the same gates, approval rules, change freezes, access limits and
   manual locks as a job that a person starts. The opt-in does not skip a
   gate. A rule of ADR 258 decision 3 that holds a remediation job for the
   approval of a person still applies to it. An installation or a namespace
   cannot use this feature to bypass a control that it has.
7. **The audit log records the job as automatic.** The record of the job
   names that the server started it, which setting allowed it, and the drift
   result that caused it. A user can list these records and filter them from
   the jobs that a person started. A job that the server decides not to start
   because of an opt-out writes no job, and the API answers the question in
   decision 5.
8. **ADR 258 decision 4 stays true.** A team can still start a remediation
   job from its own automation through the API. This decision adds the
   server-side path. It does not remove that one.

## Not decided here

- Which objects can hold the opt-in setting, and the order in which a value
  at a collection, a unit or the installation resolves when no namespace
  opt-out exists.
- The name of the setting, its wire values and the settings action that
  changes it. An RFC or the slice that implements it decides these.
- Which remediation action the job runs: accept the cloud, revert the cloud,
  or a choice of the unit. ADR 258 decision 2 names both.
- The source kind and the principal of an auto-dedrift job, and if the
  built-in gate of ADR 258 decision 3 covers it by the source kind
  `schedule` or by another rule.
- Limits on the feature, for example a delay after a drift result, a limit
  on jobs for each unit and period, and what happens after repeated failed
  remediations.
- Whether a person can see and cancel the plan of an auto-dedrift job before
  it applies, other than through the normal approval view.
- The notifications that the feature sends.
- Whether the installation can turn the feature off for all namespaces.
- The user documentation page and the console view. They ship with the slice
  that implements the feature.
- The order of the implementation. It follows the runner session routes and
  the launcher.

## Consequences

- A namespace that does nothing sees no change. Drift checks detect and
  notify, and a person starts or approves a remediation.
- A team that turns the feature on keeps its gates, approvals and freezes.
  The feature reduces manual starts. It does not reduce control.
- A namespace owner has one setting that stops the feature for the whole
  namespace, and no setting above the namespace overrides it.
- The glossary gets the term **Auto-dedrift**.
- A feature that a user can see ships with its docs page (ADR 214). The
  slice that implements auto-dedrift carries its page.
