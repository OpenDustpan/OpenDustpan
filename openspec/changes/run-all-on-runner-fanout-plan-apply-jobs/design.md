## Context

The shim turns one terragrunt run-all request into one change order with one plan job and one apply job per discovered workspace. Discovery handling stays in the first part. Dependency ordered apply starts stay in the third part.

## Decisions

1. **Fan out only through the existing change order flow.** Use only the existing change order plan and apply flow of ADR 190 decisions 5, 9 and 10 through the jobs port of ADR 196 decision 5 and the durable operations of ADR 196 decision 7. No new route starts jobs. Store the stable unit ID and the stored plan ID on each job spec at create time. A claim loads only the workspace of its stored stable unit ID, holds one workspace lock, writes the state version of that workspace, and appends the history entry of that workspace.
2. **Idempotent fan-out under the existing start keys.** Repeat the plan operation under start key <change order ID>/<revision> and the apply operation under start key <change order ID>/<revision>/apply. A repeated request reuses the started job for the same key and starts no duplicate job. A restore never restarts an apply. A new revision cancels the plan jobs of the prior revision through the plan operation.

## Structure

The NEW unexported type `fanoutBinding` with its constructor `newFanoutBinding` makes the bug class impossible: it holds only one stable unit ID and only one stored plan ID, and only its constructor makes a value after it checks both IDs. The claim path takes only a `fanoutBinding` and resolves the lock, the state write, and the history append from it.

## Risks

- A job writes state of another workspace.
- An apply job applies the wrong stored plan.
- A unit with no change starts an apply job.
- A repeated request starts a duplicate job on one unit.
