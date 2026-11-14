## Context

Job A holds an accepted disjoint claim and applies. Job D reads a data source. The server cannot prove D disjoint, so D keeps the whole-unit lock and applies after A ends. This runs after the owner accepts the RFC.

## Decisions

1. **Fail-closed data source check.** Read the saved plan at claim registration. A plan with one data source read, in any module, is strict with the case data-source. A missing or malformed plan is strict too.
2. **Whole-unit lock.** Use the existing workspace lock rows in SQL, one transaction per lock action. Lock expiry frees a lost holder.

## Structure

Unexported type strictGate. Only its constructor newStrictGate makes a value. It reads the saved plan and fails closed.

## Risks

- A wrong check misses a data source and two applies overlap.
- A lost holder keeps the lock until expiry.
