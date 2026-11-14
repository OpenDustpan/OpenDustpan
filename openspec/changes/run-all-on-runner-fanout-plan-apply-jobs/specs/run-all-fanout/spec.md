## ADDED Requirements

### Requirement: Fan out plan jobs

When the change order starts its plan phase, the server SHALL start one plan job with the operation `plan` for each unit of the revision.

#### Scenario: Three units give three plan jobs
- **WHEN** the revision holds 3 discovered workspaces.
- **THEN** the server holds one change order with 3 plan jobs, each bound to the stable unit ID of a distinct workspace.

#### Scenario: Duplicate workspace
- **WHEN** the revision lists the same workspace twice.
- **THEN** the server refuses the revision with the NEW error code `changeorder.duplicate-unit` and starts no job.

#### Scenario: Workspace of another namespace
- **WHEN** one listed workspace belongs to another namespace.
- **THEN** the server refuses the revision with the NEW error code `changeorder.foreign-unit` and starts no job.

### Requirement: Apply only changed units

When the apply request for an approved revision is accepted, the server SHALL start one apply job with the operation `apply` for each unit whose plan has a change after it checks `unit:run-apply`, or `unit:run-destroy` for a destroy unit, on each such unit.

#### Scenario: One unit without change
- **WHEN** the approved revision holds 3 stored plans and 1 stored plan reports no change.
- **THEN** the server starts 2 apply jobs and starts no apply job for the unit without change.

#### Scenario: Missing apply grant
- **WHEN** the requester lacks `unit:run-apply` on one unit, or lacks `unit:run-destroy` on one destroy unit.
- **THEN** the server starts no apply job and answers the denial for that unit.

### Requirement: Bind one claim to one workspace

While a fan-out job runs, the server SHALL bind its claim to the stable unit ID of one workspace.

#### Scenario: Apply writes only its own workspace
- **WHEN** a fan-out job claim runs.
- **THEN** the claim holds 1 lock scoped to that workspace, writes 1 new state version scoped to that workspace, and appends 1 history entry scoped to that workspace.

### Requirement: Apply names its stored plan

When an apply job starts, the server SHALL apply the stored plan of the plan job of the same unit in the approved revision.

#### Scenario: Apply checks plan identity
- **WHEN** the stored plan ID of an apply job differs from the stored plan ID of the plan job of the same unit in the approved revision.
- **THEN** the server refuses the dispatch with the NEW error code `changeorder.plan-mismatch` and repeats nothing of the plan content.

### Requirement: Failed plan stops the revision

When one plan job of the current revision fails or ends with no stored plan, the server SHALL move the revision to `plan-failed` and start no apply job.

#### Scenario: Plan job fails
- **WHEN** one plan job fails or ends with no stored plan.
- **THEN** the revision ends `plan-failed`, the server starts no apply job, and the failed unit writes 0 new state versions scoped to that unit.

### Requirement: Stop on first failed apply

When one apply job of the revision fails, the server SHALL cancel each pending apply job of the same revision.

#### Scenario: One apply fails
- **WHEN** one apply job of the revision fails.
- **THEN** the server cancels each pending apply job of the revision and starts no further apply job.

### Requirement: Idempotent fan-out

When the plan operation repeats under the same start key, the server SHALL reuse each started job for that key.

#### Scenario: Repeated plan request
- **WHEN** the plan operation repeats under start key `<change order ID>/<revision>` after a restart.
- **THEN** the server reuses each started job and starts 0 duplicate jobs.

### Requirement: New revision cancels prior fan-out

When a new plan revision is attached, the server SHALL cancel each running plan job of the prior revision.

#### Scenario: Attach cancels prior plans
- **WHEN** a principal attaches a new plan revision.
- **THEN** the server cancels each running plan job of the prior revision before it starts the plan jobs of the new revision.
