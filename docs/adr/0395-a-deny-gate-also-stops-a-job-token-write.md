# 395. A deny gate also stops a job-token write

Status: accepted · 2026-11-23. Records a decision of the project owner of 22 and 23 November 2026, in the owner's words

Changes:

- Extends ADR 335 decision 1: a `deny` gate at `state.write` refuses a write with a job token that the job model allows.
- Extends ADR 224 decision 16: a write with a job token meets the gates of `state.write` after the job model allows it.

## Context

ADR 335 decision 1 says that a deny gate on a cancel or a force-unlock stays. It names those two writes. It does not name a write with a job token.

On main, `Evaluate` in `pkg/gate/service.go` returns `jobTokenWrite` for a job token before any gate runs. `evaluateStateWrite` is not called, and no decision record is written. A `deny` gate cannot stop a running job. An administrator can only cancel the job.

The change `job-token-rules-state-write` stopped on this question. The product manager asked it in the batch of 22 November 2026.

## Decision

The project owner decided decision 1 on 22 November 2026: option 1, yes. The product manager derived decisions 2 to 4 from it on 23 November 2026. The owner has not reviewed them.

1. **A deny gate stops a job token (owner, 2026-11-22).** A `deny` gate at `state.write` refuses a job-token write. This holds when the job model allows the write. The gates run after `jobTokenWrite` allows the write. No rule passes a `deny` gate for a job token.
2. **A decision record (PM, derived from decision 1, 2026-11-23).** A job-token write that reaches the gates writes a decision record. The record has the fence of its namespace, as for a person. A job token has no break-glass pass.
3. **The job model stays first (PM, derived from decision 1, 2026-11-23).** A write that the job model refuses is refused with `identity.ErrDenied` before any gate runs. The gates cannot allow a write that the job model refuses.
4. **The way out is the gate (PM, derived from decision 1, 2026-11-23).** A person who wants a job to write again edits or removes the gate. The edit needs the actions and the fresh authentication of ADR 224 decision 12.

## Consequences

- A `deny` gate is a real stop for CI jobs. The list of gates explains each refusal of a job-token write.
- Each write with a job token that reaches the gates reads the fence and writes one record. This adds one code path in security code. The change gets the extra Claude review (rev2).
- The change `job-token-rules-state-write` carries out these decisions. The routes that `stategate.UncheckedPaths` lists stay out of scope.
- This ADR does not decide the result of a gate in error or of an approval gate for a job token. The same rules as for a write by a person apply, and a job token cannot approve.
