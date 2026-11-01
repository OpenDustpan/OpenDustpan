# 306. Break-glass: the session limit, the mass-suspension alert and the disable rules

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026 and 20 October 2026 that no ADR held

Changes:

- Extends ADR 70 decision 21: the limit and the window of the mass-suspension alert.
- Extends ADR 70 decision 24: a site admin may disable a break-glass principal.
- Extends ADR 70 decision 25: the time limit of the elevated session.

## Context

The project owner made these decisions in the owner decision files of 18 and 20 October 2026. A check on 1 November 2026 found no ADR that states them.

ADR 70 leaves two values open. One is the time limit of the elevated break-glass session. The other is the limit and the window of the mass-suspension alert. ADR 70 decisions 24 and 25 give the guard of two working paths and the rule of last resort. They do not say how a site admin disables a break-glass principal.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **The break-glass session limit is 1 hour (owner, 2026-10-18).** The owner said: "session limit 1 hour, as a setting".
2. **The mass-suspension alert has settings (owner, 2026-10-18).** The owner said: "Mass-suspension alert: 10 members or 20 percent in 15 minutes". The owner said also: "as settings, through the ops notifications (ADR 145)".
3. **A site admin may disable a break-glass principal (owner, 2026-10-20).** The owner accepted this "as written for now". The owner said: "a site admin may disable a break-glass principal, except the last enabled one (refused in the write transaction, 409)". The guard still applies.
4. **A disable has an audit entry and a notice (owner, 2026-10-20).** The owner said: "each disable is audited". The owner said also: "notifies the other admins".
5. **Re-enable and maintenance mode (owner, 2026-10-20).** The owner said: "re-enable stays checked". The owner said also: "disable works in maintenance mode".
6. **Delete and sweep (owner, 2026-10-20).** The owner said: "delete and sweep follow the same 'not the last one' rule".

## Consequences

- ADR 70 gets a pointer to this ADR.
- Decisions 1 and 2 close two items of the list "Not decided here" in ADR 70.
- Decisions 3 to 6 need a test for each step. Each test sends hostile input to that step alone.
- Open for the project manager: how decision 3 fits ADR 70 decision 25 ("No job disables a break-glass account"). Also open: what "re-enable stays checked" checks.
