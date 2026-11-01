# 291. Insights count failures by token and service principal, never by person

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026

## Context

Insights show how an installation is used. A count of failures for each person can become a tool to rank or blame people. The "No magic" rule needs the data to be explainable, and a person must not be the unit of blame.

## Decision

1. **The unit of a failure count (owner).** Insights count failures by access token and by service principal. They never count failures by person. (owner)
2. **No person key in the data.** The insight data holds no person ID for a failure. This is a defense-in-depth layer: the API does not group by person, and the stored rows do not hold a person for a failure count.
3. **Failure insights are opt-in (owner).** An installation shows failure insights only when an operator turns them on. The setting is off by default. Its effective value is in the API. A change writes an audit entry. (owner)
4. **Other insights.** This ADR does not change an insight that is not a failure count.

## Consequences

- The insight routes and their tests refuse a grouping by person for a failure.
- The user documentation says what the failure insights count and how an operator turns them on.
- A failure that a person causes still appears in the audit log and in the history of the unit, as it does today.
