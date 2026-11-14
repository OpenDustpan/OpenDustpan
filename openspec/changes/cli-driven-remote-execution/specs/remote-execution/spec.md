## ADDED Requirements

### Requirement: Configuration upload is a file set

When the CLI uploads a configuration version to an agent workspace, the server SHALL hold it as a file set through a single signed upload with a short expiry.

#### Scenario: Bad archive refused
- **WHEN** the CLI uploads an archive that fails the checks
- **THEN** the server refuses the version with a JSON:API error and never changes the archive to make it pass.

#### Scenario: Upload URL is single use
- **WHEN** the CLI uploads a configuration version through the signed URL
- **THEN** a second upload to the same URL fails closed.

### Requirement: Speculative plan on runner

When the Developer runs tofu plan on a workspace with `execution-mode` `agent`, the server SHALL start a plan job on a runner with only the fixed arguments from the allowlist.

#### Scenario: Plan streams runner log
- **WHEN** the Developer runs tofu plan on a workspace with execution-mode agent
- **THEN** the CLI streams the log of the plan phase of the job to completion.

#### Scenario: Unknown argument refused
- **WHEN** the CLI sends a plan argument outside refresh, refresh-only, target-addrs, replace-addrs and is-destroy
- **THEN** the server refuses the run with a JSON:API error.

### Requirement: Pool required

When the workspace sets `execution-mode` `agent` but the namespace has no runner pool, the server SHALL refuse the run.

#### Scenario: Missing pool refused
- **WHEN** the workspace sets agent but the namespace has no runner pool or entitlement
- **THEN** the server refuses the run with a JSON:API error that names the runner pool.

### Requirement: Gate refusal ends errored

When the gates of change propose refuse the remote plan, the server SHALL end the run as errored and name the decision record in the log.

#### Scenario: Gate refusal names record
- **WHEN** the gates of change propose refuse the remote plan
- **THEN** the run ends as errored and the log names the decision record.

### Requirement: Refuse non-speculative run

When the CLI sends a configuration version with `speculative` false, the server SHALL refuse it with a JSON:API error that names local execution.

#### Scenario: Non-speculative refused
- **WHEN** the CLI sends a configuration version with speculative false
- **THEN** the server refuses the version with a JSON:API error that names local execution.

### Requirement: Refuse apply routes

When the CLI calls an apply route on a speculative-only workspace, the server SHALL answer the speculative-only error.

#### Scenario: Apply refused
- **WHEN** the CLI calls actions/apply, actions/discard or actions/cancel of a run that is not a plan
- **THEN** the server answers the speculative-only error.

### Requirement: Variables stay secret

When the CLI sends a run variable, the server SHALL seal it in the job spec and give it only to the runner of the claim.

#### Scenario: Secret variable masked
- **WHEN** the CLI sends a run variable
- **THEN** the job log, the audit entry and the API answer hold only the mask.

### Requirement: Signed log read

When the CLI reads the log through the signed URL, the server SHALL allow only the bound job, phase and principal within its expiry after it checks job read-logs at mint and at read.

#### Scenario: Expired URL refused
- **WHEN** the CLI reads the log URL after its expiry
- **THEN** the read fails closed.

#### Scenario: Replayed URL refused
- **WHEN** a caller replays the log URL for another job or another principal
- **THEN** the read fails closed.

#### Scenario: Missing grant refused
- **WHEN** the principal holds no job read-logs grant on the unit
- **THEN** the mint and the read fail closed.

### Requirement: Poll answers without 404

When the CLI polls during a remote plan, the server SHALL answer the poll routes without 404.

#### Scenario: Poll routes answer
- **WHEN** the CLI polls run-events, task stages, queue or capacity during a remote plan
- **THEN** each route answers without 404.
