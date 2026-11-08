# 344. An agent drafts a change order, and a person confirms it

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 190 decision 14: an agent can draft a change order. A person confirms it.

## Context

ADR 190 decision 14 says that an agent never creates a change order. Each such write fails with `changeorder.ErrAgentDenied`. The agent tool set of ADR 122 has no tool for change orders.

An open question of ADR 190 asks if a later ADR gives agents a tool that creates change orders. The person who asked would confirm the result.

On 7 November 2026 the owner decided this point.

## Decision

Decided by the project owner on 7 November 2026.

1. **An agent drafts a change order (owner, 2026-11-07).**
2. **A person confirms it (owner, 2026-11-07).** The draft is not a change order until a person confirms it.
3. **The rest of decision 14 stays (owner, 2026-11-07).** An agent still records no verdict. It does not attach a plan, approve, request changes, reject or close. It does not resolve or reopen a thread. It does not ask for an apply.

## Consequences

- This ADR does not decide the state of a draft, the tool in ADR 122, the action name or the audit entry. A later ADR decides them.
- This ADR does not decide which person can confirm. It does not decide if the person who asked can confirm. It does not decide if the confirmation counts as the creation for the rules of review.
- The code and the suite that refuse `Create` by an agent (`changeorder.ErrAgentDenied`) change only when that later ADR lands.
