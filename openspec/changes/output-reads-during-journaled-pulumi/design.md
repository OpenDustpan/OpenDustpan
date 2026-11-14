## Context

Output reads must answer from one stable latest state with bounded cost. The owner export rebuilds a running journaled update from stored batches. Consumer output reads stay on the last completed checkpoint.

## Decisions

1. **Latest state for output reads excludes the running journal.** Latest state for consumer output reads is the last completed checkpoint. Batches of a running journaled update are not part of latest state until the update completes and writes a new checkpoint. The owner export behavior does not change.

## Structure

The output read path loads the last completed checkpoint by its stable state version id through one constructor that refuses a running update id, so the read cannot load a partial state.

## Risks

- A consumer reads outputs that are older than the running update until the update completes.
