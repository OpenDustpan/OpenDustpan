# 306. Break-glass: the session limit, the mass-suspension alert and the disable rules

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 and 20 October 2026

Changes:

- Changes ADR 70 decisions 24, 25 and 26: a site admin may disable a break-glass principal. The sweep may disable or delete one.
- Extends ADR 70 decision 21: the limit and the window of the mass-suspension alert.

## Context

The project owner made these decisions in the owner decision files of 18 and 20 October 2026. A check on 1 November 2026 found no ADR that states them.

ADR 70 leaves two values open. One is the time limit of the elevated break-glass session. The other is the limit and the window of the mass-suspension alert. ADR 70 decisions 24, 25 and 26 give the guard of two working paths, the rule of last resort and the record of each use.

The break-glass disable rules of 20 October 2026 are a summary of the product manager. The owner accepted them "as written for now". They are later than ADR 70 (accepted 2026-10-15). They win over decisions 24, 25 and 26 of ADR 70.

This ADR writes the words of the owner. It adds no decision of its own, except the two items marked "(PM, for the owner)".

## Decision

1. **The break-glass session limit is 1 hour (owner, 2026-10-18).** The owner said: "session limit 1 hour, as a setting".
2. **The mass-suspension alert has settings (owner, 2026-10-18).** The owner said: "Mass-suspension alert: 10 members or 20 percent in 15 minutes". The owner said also: "as settings, through the ops notifications (ADR 145)".
3. **A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now).** This changes ADR 70 decision 24. The server refuses only a change that leaves no enabled break-glass principal. The accepted text: "a site admin may disable a break-glass principal, except the last enabled one (refused in the write transaction, 409)". "The guard still applies". "each disable is audited and notifies the other admins". "re-enable stays checked". "disable works in maintenance mode". "delete and sweep follow the same 'not the last one' rule". (PM, for the owner): the server writes the 409 refusal to the audit log. The rule goes in the catalog of code rules.
4. **Audit and notice (owner, 2026-10-20, accepted as written for now).** Each disable writes an audit entry. Each disable notifies the other admins.
5. **Re-enable and maintenance mode (owner, 2026-10-20, accepted as written for now).** Re-enable stays checked. Disable works in maintenance mode.
6. **Delete and sweep (owner, 2026-10-20, accepted as written for now).** Delete and sweep follow the same "not the last one" rule. This changes ADR 70 decisions 25 and 26. The sweep may disable or delete a break-glass principal. (PM, for the owner): each disable or delete by the sweep writes an audit record. The record names the sweep, the account and the reason.

## Consequences

- ADR 70 gets pointers to this ADR.
- Decisions 1 and 2 close two items of the list "Not decided here" in ADR 70.
- Decisions 3 to 6 need a test for each step. Each test sends hostile input to that step alone.
