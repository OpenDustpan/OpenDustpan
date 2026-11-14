## Context

Inventory shows per-unit states from several writers. This change adds the job-model writes for running, queued, awaiting-approval, failed and apply-needed, and the stack-package write for failed, each in the transaction of its own write with the fence. The states slice owns the states field, the filter and the badge order.

## Decisions

1. **One stored fact per unit per state kind.** Store one row per unit per state kind for running, queued, awaiting-approval, failed and apply-needed with unit ID, state kind, change identity, end time and since time. Writers upsert or delete only their own kinds in the same transaction as the job change, update end or plan validity change, with the fence checked in that transaction. A fence failure rolls back the whole transaction including the job change. The states slice reads these rows.
2. **End-time ordering with fail-closed same-change disagreement.** A failed change end sets failed with its change identity and end time. Only an end with a later end time overwrites the failed row. A single end sets failed when its counterpart never arrives, using its own end time. When the job end and the update end carry the same change identity and disagree, failed stays until a later change identity with a later end time and success clears it. Cancelled, timed out, lost-claim and superseded ends clear live states and leave failed unchanged. Reconcile keeps rows whose job identity is still live and clears rows whose job is terminal or unknown. A ready stored plan with changes sets apply-needed; apply, supersede by a newer plan, expiry or invalid plan clears it. With two ready plans the unit keeps apply-needed until no ready plan with changes remains. No acknowledge route exists.

## Structure

Unexported inventory state writer with constructor that takes the store, clock and fence reader; only the constructor makes valid state rows and each write method checks the fence before the upsert.

## Risks

- Two writers of failed can disagree on success; the fail-closed rule keeps failed until a later success clears it.
- A stale plan can keep apply-needed; supersede, expiry and invalid-plan clears bound its life.
