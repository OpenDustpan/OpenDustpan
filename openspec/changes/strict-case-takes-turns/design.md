## Context

Job A holds an accepted disjoint claim and applies. Job D reads a data source. The server cannot prove D disjoint, so D needs the whole-unit lock, ends as blocked while A applies, and starts again when the lock is released. This runs after ADR 368 is on main.

## Decisions

1. **Fail-closed data source check.** Read the saved plan at claim registration. A plan with one data source read, in any module, is strict with the case data-source. A missing or malformed plan is strict too.
2. **Whole-unit lock.** Use the existing workspace lock rows in SQL, one transaction per lock action. Lock expiry frees a lost holder.
3. **Taking turns.** A strict runner job that finds the lock held turns the refusal into the status blocked (ADR 302 decision 2). The release of the lock starts the blocked job again through the job service, with an audit record that names the released lock. The local wrapper waits with the retry and timeout of ADR 368 decision 5.

## Structure

Unexported type strictGate. Only its constructor newStrictGate makes a value. It reads the saved plan and fails closed.

## Risks

- A wrong check misses a data source and two applies overlap.
- A lost holder keeps the lock until expiry.
