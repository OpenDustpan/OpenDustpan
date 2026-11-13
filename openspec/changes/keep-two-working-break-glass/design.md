## Context

The guard must make the loss of the last working path impossible under concurrent writes. The setup must make an install with fewer than two accounts impossible to finish.

## Decisions

1. **One count function.** One function in pkg/principal counts working accounts with the four properties of ADR 70 decision 24 and the passkey or hardware key of decision 23. The guard, the setup and the restore check call it. It reads the final rows of the transaction, in a fixed order of checks.
2. **Two rules by change kind.** The guard takes a change kind. Disable, delete and sweep use the rule that one enabled account must stay. Every other covered change, a passkey removal, an expiry change and a policy block included, uses the floor of two working paths. A change that does not lower the count is never refused. A change kind with both effects uses the disable rule for the account and the floor for the credential.
3. **Row lock and busy answer.** The guard locks the break-glass rows in the write transaction in one fixed order, the same for restore and setup finish, so a deadlock does not occur. A lock timeout returns break-glass.busy as 409 and writes nothing. A retry is safe, also for setup finish. A crash before commit rolls back every write. A break-glass sign-in takes no guard lock, so a busy answer never leaves a half-written sign-in.
4. **Restore check before the swap.** The restore runs the count on the staged data before the swap, per scope as ADR 216 decision 13 says, then the swap runs under the lock. A restore with fewer than two working paths is refused. Tests cover an old backup, a two-account backup and a hosted namespace backup.
5. **Atomic rotation.** Rotation inserts the new credential and ends the old one in one transaction. A failed insert rolls back and the old credential stays active. A credential change that removes the last working credential of an enabled account lowers the count and follows the floor.
6. **Fresh authentication.** Rotation, add and each guarded change of a break-glass account need a fresh authentication in the one window. A person with no factor gets the enrollment hint of the refusal (Owner decision, 13 Nov 2026 (queue P19) keeps the floor wherever the newer text is silent).

## Structure

An unexported type breakGlassSet in pkg/principal. Only its constructor loads the locked rows. Its methods requireNotLast and requireFloor return the sentinels ErrLastBreakGlass and ErrBreakGlassFloor. Each covered write takes the set from the transaction, so no write path skips the count.

## Risks

- Two parallel disables read the same count. The row lock removes the race.
- A restore of an old backup drops accounts. The restore guard runs on staged data before the swap.
- A passkey removal lowers the count. The count includes the passkey property and the removal uses the floor.
- A deadlock with restore or setup finish. One lock order removes it.
- Two setup finishes in parallel. The same lock and the same count serialize them.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service builds the set in the write transaction and returns the sentinel by change kind. | Disable the last two accounts in parallel. One request fails with 409 and one account stays enabled. |
| store adapter | The store checks the count and the fence in the same SQL transaction with row locks. | Call the store directly to delete the last enabled account. The store refuses. |
| HTTP handler | The handler declares the action on the break-glass object, requires a fresh authentication and maps each sentinel to 409. | Send a disable with a stale fresh step. The handler answers 403 before the service runs. |
