## ADDED Requirements

### Requirement: An apply job starts and waits for the approval default

The server SHALL accept the operations `apply` and `destroy` for an OpenTofu workspace on a runner. When an approval default covers the workspace, the job SHALL wait in `awaiting-approval` after its plan phase, and the server SHALL dispatch the apply phase only after the approvals that the default requires.

#### Scenario: Apply waits then continues
- **WHEN** a person starts an apply job on a workspace that an approval default of two approvers covers, and the plan phase ends
- **THEN** the job is in `awaiting-approval`, and after two distinct approvers send a verdict with the binding of the stored plan the job is queued and the server dispatches the apply phase

#### Scenario: Apply with no default needs no approval
- **WHEN** a person starts an apply job on a workspace that no approval default covers
- **THEN** the job waits for no approval and the apply phase dispatches after the plan phase

#### Scenario: Another tool keeps its refusal
- **WHEN** a person starts an apply job on a stack of the Pulumi CLI
- **THEN** the server answers 422 with the code `job.unsupported` and stores no job

### Requirement: The dispatch rechecks the approval defaults and only adds

Before the server dispatches a queued job, it SHALL read the approval defaults of the unit again. A default that adds a requirement SHALL put the job back in `awaiting-approval` with a reason that names the default. A default that was removed or lowered SHALL NOT release a job that waits.

#### Scenario: A default saved after the start blocks the dispatch
- **WHEN** an administrator saves an approval default after a job started without a gate, and the job is queued to dispatch its apply phase
- **THEN** the job goes to `awaiting-approval` and the apply phase does not dispatch

#### Scenario: A removed default does not release the wait
- **WHEN** an administrator removes the approval default while a job waits in `awaiting-approval`
- **THEN** the job stays in `awaiting-approval` until the stored gate has its approvals

#### Scenario: The recheck fails closed
- **WHEN** the server cannot read the approval defaults at the dispatch
- **THEN** the job is not dispatched and the reason of the job says that the defaults cannot be read

### Requirement: The audit entry of a start names the policy version

When the policy of a start decides, the audit entry of the start SHALL name the policy version of the decision.

#### Scenario: The entry names the policy
- **WHEN** a job starts under the approval defaults
- **THEN** the audit entry of the start holds the policy version `approval-defaults`
