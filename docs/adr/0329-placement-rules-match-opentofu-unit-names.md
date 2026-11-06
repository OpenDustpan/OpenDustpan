# 329. Placement rules match OpenTofu unit names

Status: accepted · 2026-11-06. Decided by the project owner on 6 November 2026

Changes:

- Extends ADR 61 decisions 1 and 3: placement rules also match OpenTofu unit names. The server refuses a unit that no rule matches.

## Context

[ADR 61](0061-collection-of-a-unit-from-the-cli.md) gives placement rules for Pulumi stacks. For OpenTofu, the `project` of the `cloud` block names the collection. The OpenTofu HTTP backend has no such name, and today it creates a unit on its first POST.

On 6 November 2026, about 13:55 UTC, the owner wrote: "All are approved by me as proposed." This ADR records decision OD-2 (milestone 4 placement). The text of the decision is word for word as the owner approved it.

## Decision

Decided by the project owner on 6 November 2026.

1. **Placement rules also match OpenTofu unit names (owner).** Placement rules also match OpenTofu unit names.
2. **Fail closed (owner).** The server refuses a unit that no rule matches. The error names the missing rule.
3. **The OpenTofu HTTP backend (owner).** This includes the OpenTofu HTTP backend. Today it creates a unit on its first POST.

## Consequences

- This ADR does not decide the syntax of a rule for an OpenTofu unit name, or how it relates to the `project` of the `cloud` block. These points are not part of the owner decision.
