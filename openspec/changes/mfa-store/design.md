## Context

The storage half of built-in MFA: the adapters of the pending sign-in, the recovery codes and the TOTP factor, with their backup rules.

## Decisions

1. **The stores come first.** The SQLite and PostgreSQL adapters of `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore` land here, with the conformance suites first. The change `mfa` starts when they are on main.
2. **The sign-in counts apart from the step-up.** The new method `AcceptSignInStep` of `stepup.TOTPStore` accepts a time step with the single-use rule of `AcceptStep` and touches no step-up counter, so a step-up lockout never blocks a sign-in. It gets conformance tests before any adapter implements it.
3. **Each new table has one backup rule, each after an existing rule.** The pending sign-in table is an emptied table: its rows live for minutes and a restore cancels the pending sign-ins. The lock is on the factor row, so a restore that replaces access copies the live lock state of each factor into the restored factor row and takes the stricter value of each field, so that a restore cannot end a lock; the attempts table is a control table (a backup leaves it out and a restore keeps its live rows), and the same step deletes the live attempts rows of a factor that the restored data does not have and writes one audit entry with the count. The factor table is an access table, as `passkeys` is.

## Structure

The pending sign-in is `mfa.Pending`, kept in `mfa.PendingStore`. This change adds no second pending type.

## Risks

- A restore of an older backup could make a used recovery code valid again. The backup rules and their hostile tests prevent it.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Store adapter | Keep digests and the sealed secret, never a plain code. | Read each row; no plain code. |
| Store adapter | Accept a time step once. | Reuse a step; the second call fails. |
| Restore | Keep the live lock and the used recovery codes. | Restore an older backup; the lock and the used codes stay. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 0 used recovery codes valid again after a restore | codes | restore |

## Contract

**stepup.TOTPStore**

- `AcceptSignInStep`: pre: The factor is active. Post: A higher time step is stored; an equal or lower step returns `stepup.ErrUsed`; no step-up counter changes.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Accept a time step | Two calls with one step | The loser uses a new code. | One call wins. |
| Restore | Older backup | None. | Locks and used codes stay. |
