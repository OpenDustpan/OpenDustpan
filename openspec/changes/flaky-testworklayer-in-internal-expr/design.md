## Context

TestWorkLayer fails on loaded runners when run takes just over 1 s. The test asserts a tight wall-clock bound instead of the behavior that the limit stops the work.

## Decisions

1. **Replace tight bound with tolerant window.** Change only the file named in what_changes. Keep the production 1 s limit. Assert the work started, the result is the timeout error, and elapsed is at least 900 ms and less than 30 s. No fake clock and no production change.

## Structure

No new type. No structure removes a tight test bound without changing production code, so a tolerant assertion takes its place for this test only.

## Risks

- A 30 s upper bound hides a slowdown between 1 s and 30 s. The lower bound plus the timeout-error assertion still catch a missing limit, and the mutation task proves it.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/expr]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only change with no user-visible latency or storage effect |  |  |
