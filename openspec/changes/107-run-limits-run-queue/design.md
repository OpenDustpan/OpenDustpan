## Context

An operator runs many stacks and workspaces on few runners. One team can use all capacity unless the installation sets a cap and each namespace sets a limit. A run that starts when limits are full waits in a queue with a visible position.

Main has no run model yet. A held-unit queue for job starts and a durable scheduler with a first-claim delay exist, but neither is a run limit with a queue order. This change waits on the run model of milestone 5. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Limits and the queue ship with the run model. Raw job starts keep the current held-unit behavior until then.
2. **Limits live on the one settings resource.** The installation cap and each per-namespace limit are settings of their scope, with effective value and source shown (ADR 212, decision 7). A raise of a limit needs a fresh authentication and writes an audit entry. A lower limit needs no fresh step and applies at once.
3. **Strict FIFO per namespace first.** Queued runs start in the order they arrived. Per-unit limits and lanes for plans and applies stay as later steps.
4. **A slot counts only while a live lease holds it.** The last slot is claimed in one transaction with the fencing token, so two starters cannot take it (ADR 72, decision 2). A lowered limit stops new starts and leaves running runs alone.
5. **Fail closed when the target cannot run.** A queued run whose unit is deleted, suspended, or restoring ends with a named error. Short holds keep the run queued.
6. **Queue work runs on the primary installation only.** Each queue write carries the namespace fence with the maintenance check in the same transaction (ADR 248, decision 14; ADR 72, decision 4).

## Structure

No new structure now. The bug class is a second limit store beside the settings resource; the one settings resource plus durable operations with a lease per operation is the structure that removes it. When the run model ships, the queue claim is an unexported record built only through its constructor, which checks the lease, the fencing token, and the fence.

No custom analyzer and no new tool. A later refinement names the constructor and its checks.

## Risks

- Operators cannot cap runs until milestone 5 ships. The docs page for capacity says so and points at runner pools instead.
- A stuck queued run blocks its namespace lane. A namespace administrator cancels a queued run by its ID, which writes an audit entry.
- A lowered limit reads as lost capacity. The effective value and source stay visible on the settings resource.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| settings | Raise of a limit needs a fresh authentication; each change writes an audit entry with actor and source. | Raise a limit with an old session; the server refuses and changes nothing. |
| service | Count a slot only while its lease is live; claim the last slot in one transaction with the fencing token. | Start two runs at the last slot; one wins and the other stays queued. |
| store | Carry the fence and the maintenance check on each queue write. | Write a queue row without a fence; the store refuses it. |
| handler | Fail closed with a named error on delete, suspend, and restore; keep queue position visible. | Start a queued run for a deleted unit; the answer names the error, not a retry. |
