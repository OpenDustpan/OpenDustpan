## Context

The guard must make the loss of the last working path impossible under concurrent writes. The setup must make an install with fewer than two accounts impossible to finish.

## Decisions

1. **One count function.** One function in pkg/principal counts working accounts with the four properties and the passkey or hardware key. The guard, the setup and the restore check call it. It reads the final rows of the transaction.
2. **Two rules by change kind.** Disable, delete and sweep use the rule that one enabled account must stay. Every other covered change uses the floor of two. A change that does not lower the count is never refused (Owner decision, 13 Nov 2026 (queue P19)).
3. **Row lock and busy answer.** The guard locks the break-glass rows in one fixed order, the same for restore and setup finish. A lock timeout returns break-glass.busy as 409 and writes nothing. A sign-in takes no guard lock.
4. **Restore check before the swap.** The restore counts the staged data per scope before the swap, then the swap runs under the lock.
5. **Atomic rotation.** Rotation inserts the new credential and ends the old one in one transaction. A failed insert rolls back.
6. **Fresh authentication.** Rotation, add and each guarded change need a fresh authentication in the one window.

## Structure

An unexported type breakGlassSet in pkg/principal. Only its constructor loads the locked rows. Its methods requireNotLast and requireFloor return the sentinels ErrLastBreakGlass and ErrBreakGlassFloor. Each covered write takes the set from the transaction, so no write path skips the count.

## Risks

- Two parallel disables read the same count. The row lock removes the race.
- A restore of an old backup drops accounts. The restore check runs before the swap.
- A deadlock with restore or setup finish. One lock order removes it.
- Other e2e steps fail at first setup. The shared fixture makes two accounts.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service builds the set in the write transaction and returns the sentinel by change kind. | Disable the last two accounts in parallel. One request fails with 409. |
| store adapter | The store checks the count and the fence in the same SQL transaction with row locks. | Call the store directly to delete the last enabled account. The store refuses. |
| HTTP handler | The handler declares the action, requires a fresh authentication and maps each sentinel to 409. | Send a disable with a stale fresh step. The handler answers 403 before the service runs. |
