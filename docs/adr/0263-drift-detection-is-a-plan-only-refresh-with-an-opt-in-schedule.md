# 263. Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule

Status: accepted · 2026-10-20. Records decisions 25.4 and 25.5 of the owner of 18 October 2026. Answers part of RFC 25 and open question 2 of ADR 161. Extends ADR 258 decision 1 and the phase table of ADR 100 decision 4

## Context

[RFC 25](../rfc/0025-drift-detection.md) proposes drift detection: a
scheduled refresh-only run for each unit, a status in the inventory, and
actions on a drift. [ADR 258](0258-drift-remediation-is-a-plan-that-a-person-approves.md)
decided the remediation half. A drift run only plans, and a remediation is a
separate job that a person approves.

The owner decided on 18 October 2026:

- 25.4: a unit has the status `drifted`, and the context read API has a
  section for drift.
- 25.5: the drift schedule is off by default and opt in. The frequency comes
  from a list or from a custom cron expression.

[ADR 161](0161-one-context-read-api.md) asks in open question 2 whether drift
goes into the section `health` or into a section of its own.

The mechanism, the status values, the wire names, the result fields, the
limits and the slices stay open in RFC 25. This ADR holds only what is
decided.

## Decision

1. **A drift check is a job.** It has the operation `refresh` and one
   phase, `plan`. For OpenTofu it runs `plan -refresh-only
   -detailed-exitcode`. For the Pulumi CLI it runs `refresh --preview-only`.
   It changes no resource and writes no state version. The phase table of
   ADR 100 decision 4 gets this row:

   | Executor | Operation | Phases |
   | --- | --- | --- |
   | runner | refresh (drift check) | plan |

   A plan phase never holds the unit (ADR 100 decision 10). So a drift check
   never blocks a change.
2. **The drift check sets the status `drifted`.** When a check finds a
   difference between the state and the real resources, the unit shows the
   status `drifted` in the inventory. A clean check clears it. The status
   comes from the drift result that the check stored. The other statuses
   and the wire values are the work of RFC 26.
3. **The server stores the drift result.** The result holds the addresses of
   the resources that differ. The unit keeps the result of its latest check.
   The job history keeps the older checks.
4. **The context read API has a section of its own for drift.** Drift does
   not go into `health`. This answers open question 2 of ADR 161. A server
   that has no drift feature answers `unavailable` for the section, as
   ADR 161 says for any section whose feature is not there.
5. **A drift check starts on its own only when a person turned it on.**
   The setting is off by default. A unit has the setting, and a collection
   or a namespace can give a default. The frequency comes from a list or
   from a custom cron expression. With no setting, no drift check starts
   on its own.
6. **A change to `drifted` sends a notification.** It uses the model of
   ADR 145. The slice that sends it adds the type to the catalog of
   notification types.
7. **Out of scope: automatic remediation.** ADR 258 holds the rules.

## Consequences

- The detection slices can ship with no remediation. A drift check has no
  apply phase, so it needs no approval, and ADR 258 decision 3 has nothing
  to hold in it. RFC 25 asks that the scope of the built-in gate says so in
  plain words.
- ADR 205 decision 4 and ADR 226 decision 7 say that a `refresh` has no
  plan phase. That stays true for a manual refresh and is not true for a
  drift check.
- The glossary gets the terms **Drift**, **Drift check**, **Drift result**
  and **Drift schedule**.
- A feature that a user can see ships with its docs page (ADR 214). Each
  slice of RFC 25 carries its page.
