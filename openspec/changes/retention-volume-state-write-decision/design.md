## Context

Each state.write evaluation writes one decision record in the same transaction as the step it explains, except a refusal which uses its own transaction. OpenTofu lock retries caused one evaluation per free-lock attempt, so records grow with no prune.

## Decisions

1. **Keep every evaluation record.** Keep one record per evaluation, including allow results with no gate, per ADR 222 decision 3. A retry against a held lock runs no evaluation and writes no record.
2. **Prune by audit retention with a single holder.** Add a retention sweep that deletes state.write decision records past the audit retention, oldest first by stable record ID, in bounded batches with one holder at a time through a store lease, safe to restart mid-batch. The sweep reads the audit retention at sweep time, so a retention change applies to records already written. The sweep never deletes a record whose explained step is still open or whose step audit entry is missing.
3. **Keep the audit reference after prune.** A prune batch writes one audit entry that names each deleted record ID. The audit entry of the explained step keeps its record ID after the record is pruned, so the named ID stays traceable. State writes proceed while the sweep holds its lease; the sweep lease guards only the sweep, never the write path.

## Structure

Unexported gate record store with constructor-checked retention: type recordSweeper holds auditRetention and batchSize, and only recordSweeper.NextBatch selects expired state.write records by stable record ID for delete.

## Risks

- Lock retry storms still write one record per free-lock attempt before the sweep removes them; the sweep bounds growth over time by cadence and batch size, with no per-change cap on record writes.
- A large backfill of old records makes the first sweep run many batches; bounded batches and the single-holder lease keep each batch small and restartable.
