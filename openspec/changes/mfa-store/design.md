## Context

The storage half of built-in MFA: the adapters of the pending sign-in, the recovery codes and the TOTP factor, with their backup rules.

## Decisions

1. **The stores come first.** The SQLite and PostgreSQL adapters of `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore` land here, with the conformance suites first. The change `mfa` starts when they are on main.
2. **The sign-in counts apart from the step-up, and has its own limit.** The new method `AcceptSignInStep` of `stepup.TOTPStore` accepts a time step with the single-use rule of `AcceptStep` and touches no step-up counter, so a step-up LOCK (temporary) never blocks a sign-in. A factor that the step-up failures disabled (the upper limit of wrong step-up codes, 100) fails closed: `AcceptSignInStep` returns `stepup.ErrDisabled` for any code until a reset, and appends one audit entry for the refusal. It gets conformance tests before any adapter implements it. Main has no bound on wrong TOTP codes at the sign-in across pending sign-ins: `mfa.PendingMaxFailures` (pkg/mfa/mfa.go:75) bounds the wrong factors of one pending sign-in only, and `sign_in_failures` and the authgate rules count by source address, not by person or factor. A password holder could start a new pending sign-in after each sign-in and get five guesses for each one. So the factor gets its own sign-in failure count, with the NEW constants `stepup.TOTPSignInMaxFailures` (5) and `stepup.TOTPLockDuration` (15 minutes, provisional: no such constant exists on main, and the owner may change it). The NEW method `FailSignInStep` of `stepup.TOTPStore` counts one failed sign-in TOTP check in the transaction that reads the count, and sets the sign-in lock at the fifth. While the sign-in lock holds, `AcceptSignInStep` returns `stepup.ErrLocked` for any code, also a good one, and appends one audit entry for the refusal. A success sets the count to 0. The sign-in count and lock are separate from the step-up count and lock: neither one changes the other. A restore carries the stricter value of the new columns (task 3.6).
3. **Each new table has one backup rule, each after an existing rule.** The pending sign-in table is an emptied table: its rows live for minutes and a restore cancels the pending sign-ins. The lock is on the factor row, so a restore that replaces access copies the live lock state of each factor into the restored factor row and takes the stricter value of each field, so that a restore cannot end a lock; the attempts table is a control table (a backup leaves it out and a restore keeps its live rows), and the same step deletes the live attempts rows of a factor that the restored data does not have and writes one audit entry with the count. The factor table is an access table, as `passkeys` is. The recovery code table is an emptied table: a restore that replaces access writes one audit entry that gives the count only when it removes at least one code, and writes none for zero codes or in an ordinary swap, because no action took place; the count lives only in the replace-access path. The dev strip gets one class, for the factor table: it runs only on a staged file, and a staged file holds a table for every table of the schema, but rows only for the tables that a backup carries, so the attempts, recovery code and pending sign-in tables never hold a row there.

4. **No revoke yet, and the promise stays.** No step-up credential type, table or issuer exists, so `ConfirmTOTP` and `ResetTOTP` revoke nothing in this change, and their port comments say so. The change that adds the step-up credential table must implement the revoke in `ConfirmTOTP` and `ResetTOTP` in the same transaction as the delete of the factor, in that same change, and must move the two revoke cases into `stepuptest.RunTOTP`. No route may issue a step-up credential before then.
5. **The TOTP cases run in their own runner.** `stepuptest.Run` needs one store with each step-up port. The NEW runner `stepuptest.RunTOTP` runs the TOTPStore cases and the `AcceptSignInStep` cases against a harness of the TOTP store, `audit.Store` and `principal.RecordStore`. `stepuptest.Run` stays unwired until the adapters of `KeyStore`, `ChallengeStore` and `ResetStore` exist. Each case creates its persons first.
6. **One value for the completion.** `CompletePending` has six parameters and the linter allows 5, with no linter suppression comment and no change of the lint configuration. The value `mfa.Completion` holds the session, the digest of its secret and the audit entry with unexported fields, and `mfa.NewCompletion` is the only way to build it, so a zero session ID, a zero digest or an entry with no action never reaches an adapter. The adapter still checks its input (the layers below).

## Structure

The pending sign-in is `mfa.Pending`, kept in `mfa.PendingStore`. This change adds no second pending type.

## Risks

- A restore of an older backup could make a used recovery code valid again. The backup rules and their hostile tests prevent it.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Store adapter | Keep digests and the sealed secret, never a plain code. | Read each row; no plain code. |
| Store adapter | Accept a time step once. | Reuse a step; the second call fails. |
| Store adapter | Lock the sign-in of a factor after 5 failed checks across pending sign-ins. | Fail 5 times with 5 pending sign-ins; a good code then fails with `stepup.ErrLocked`. |
| Store adapter | Delete the earlier factor in the transaction of a confirm or a reset. | Confirm a new factor or reset, and an accept with the earlier factor fails with `stepup.ErrNotFound`. |
| Restore | Keep the live lock and the used recovery codes. | Restore an older backup; the lock and the used codes stay. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 0 used recovery codes valid again after a restore | codes | restore |

## Contract

**stepup.TOTPStore**

- `AcceptSignInStep`: pre: The factor is active. Post: While the sign-in lock holds, it returns `stepup.ErrLocked`, appends one refusal entry and changes nothing else. Otherwise a higher time step is stored and the sign-in count is 0; an equal or lower step returns `stepup.ErrUsed`; no step-up counter changes.
- `FailSignInStep`: pre: The factor is active. Post: The sign-in count is one higher; at `stepup.TOTPSignInMaxFailures` the sign-in lock is set until `stepup.TOTPLockDuration` after the call, and one entry is appended. While the lock holds, it counts nothing, appends one refusal audit entry in the same transaction, and returns `stepup.ErrLocked`. No step-up counter changes.

**mfa.PendingStore**

- `CompletePending(ctx, d, at, c)`: pre: `c` comes from `mfa.NewCompletion`, which returns `mfa.ErrInvalid` for a zero session ID, a zero digest or an entry with no action. Post: as before, with the session, its digest and the entry taken from `c`.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Accept a time step | Two calls with one step | The loser uses a new code. | One call wins. |
| Sign in with a factor | 5 failed checks across pending sign-ins | Wait for `stepup.TOTPLockDuration`. | The sign-in of the factor is locked; the step-up is not. |
| Restore | Older backup | None. | Locks and used codes stay. |
