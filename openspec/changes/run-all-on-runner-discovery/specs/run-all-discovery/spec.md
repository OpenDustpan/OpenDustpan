## ADDED Requirements

### Requirement: start-discovery-job

When a runner claims a terragrunt run-all wrapper job, the server SHALL start one discovery job in the same namespace.

#### Scenario: Runner claims wrapper command
- **WHEN** a runner with a pool token for a namespace claims a terragrunt run-all wrapper job
- **THEN** the server records one discovery job with operation terragrunt-discover in the same namespace.

### Requirement: preview-output

When discovery completes, the server SHALL return a preview that lists each discovered unit reference and the total count.

#### Scenario: Discovery lists units
- **WHEN** discovery finds 3 Terragrunt units
- **THEN** the job output lists each discovered unit reference with the total count and the effective limit of 200 units.

### Requirement: preview-unit-reference

Where discovery lists units, the server SHALL express each unit as a unit reference of tool OpenTofu with the namespace ID and the workspace name.

#### Scenario: Preview names workspaces
- **WHEN** discovery lists a Terragrunt unit
- **THEN** each preview row names tool OpenTofu, the namespace ID, and the workspace name, stored by stable ID.

### Requirement: refuse-escape

When the discovery path escapes the working directory after symlink resolution, the server SHALL refuse the discovery job with the NEW error code `run.discovery-refused`.

#### Scenario: Path escapes working directory
- **WHEN** the discovery path escapes the working directory after symlink resolution
- **THEN** the launcher refuses the discovery job with the NEW error code `run.discovery-refused` and writes no discovery record.

### Requirement: refuse-limit

When discovery finds more than 200 units, the server SHALL refuse the discovery job with the NEW error code `run.discovery-refused`.

#### Scenario: Too many units
- **WHEN** discovery finds more than 200 units
- **THEN** the server refuses the discovery job with the NEW error code `run.discovery-refused` and names the limit 200 units per discovery job.

### Requirement: refuse-foreign-token

When a runner token from another namespace claims a discovery job, the server SHALL refuse the claim with the NEW error code `run.discovery-refused`.

#### Scenario: Token from another namespace
- **WHEN** a runner token from another namespace claims a discovery job
- **THEN** the server refuses the claim with the NEW error code `run.discovery-refused` and starts no discovery job.

### Requirement: discovery-time-limit

When discovery runs longer than 300 seconds, the server SHALL mark the discovery job failed with the NEW error code `run.discovery-refused`.

#### Scenario: Discovery exceeds time limit
- **WHEN** discovery runs longer than 300 seconds
- **THEN** the server marks the discovery job failed with the NEW error code `run.discovery-refused` and writes no preview.

### Requirement: discovery-timeout-end-state

When the runner crashes or loses its lease before discovery completes, the server SHALL keep the discovery job failed and write one failed audit entry.

#### Scenario: Runner crashes mid-discovery
- **WHEN** the runner crashes or loses its lease before discovery completes
- **THEN** the discovery job stays failed, the audit log holds the start entry and one failed entry, and a retry with the same idempotency key returns the same failed record.

### Requirement: sandbox-denies-state-write

While discovery runs, the server SHALL write no state version and no history entry.

#### Scenario: Step tries state write
- **WHEN** a discovery step tries to write a state version
- **THEN** the launcher sandbox denies the write and discovery writes no state version and no history entry.

### Requirement: audit-discovery

When a discovery job starts and completes, the server SHALL write one start audit entry and one complete audit entry with the reason terragrunt run-all fan-out.

#### Scenario: Discovery starts and ends
- **WHEN** a discovery job starts and completes
- **THEN** the audit log holds one start entry and one complete entry that name the discovery job ID, the requesting principal, and the reason terragrunt run-all fan-out.

### Requirement: audit-refusal

When discovery is refused, the server SHALL write one refused audit entry with the refusal reason.

#### Scenario: Discovery refused
- **WHEN** discovery is refused for path escape, count limit, foreign token, or timeout
- **THEN** the audit log holds one start entry and one refused entry with the refusal reason, and no complete entry.

### Requirement: repeat-check

When the same idempotency key arrives twice, the server SHALL return the same discovery record ID.

#### Scenario: Same idempotency key repeats
- **WHEN** the same idempotency key arrives twice
- **THEN** the server returns the same discovery record ID with no second discovery record.

### Requirement: idempotency-payload-mismatch

When the same idempotency key arrives with a different discovery path or namespace, the server SHALL refuse the second request with the NEW error code `run.discovery-refused`.

#### Scenario: Same key different payload
- **WHEN** the same idempotency key arrives with a different discovery path or namespace
- **THEN** the server refuses the second request with the NEW error code `run.discovery-refused` and keeps the first record unchanged.
