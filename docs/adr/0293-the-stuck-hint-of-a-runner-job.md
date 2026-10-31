# 293. The stuck hint of a runner job

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026

## Context

A job on a runner can stop to write output. The console can show a hint for this. A rule that compares a job with earlier jobs is hard to explain. It can be wrong for a job that is slow on purpose.

## Decision

1. **One rule (owner).** The hint "stuck" shows when a running job has had no output for 10 minutes. There is no other rule. There is no rule that compares the time with earlier jobs. (owner)
2. **A setting with an effective value (owner).** The 10 minutes are a setting. The API shows its effective value and its source. A change writes an audit entry. (owner)
3. **A hint, never a status (owner).** "Stuck" is a hint. It is never a status word. The status of the job does not change. The executor or a person changes the status. The hint does not. (owner)
4. **The hint names its reason.** The hint says "No output for 10 minutes". The number comes from the setting. The hint also shows the time of the last output.

## Consequences

- The job API adds the time of the last output and the hint. It does not add a status.
- The user documentation says that the hint is not a status, and where the setting is.
