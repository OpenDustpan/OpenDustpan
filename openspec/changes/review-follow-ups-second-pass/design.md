## Context

Each row came from text matching and may be fixed on main. Check each row first, then harden only what still applies.

## Decisions

1. **Check first, then harden.** Apply only test hardening, error text wording, and comment fixes. Record each skipped row with its reason in the pull request description.

## Structure

No production structure change and no new type; unexported types and constructors are unchanged.

## Risks

- A row may already be fixed on main; the check-first task finds it before any edit.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test and comment hardening with no latency or storage change |  |  |
