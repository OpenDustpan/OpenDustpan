## ADDED Requirements

### Requirement: Job limit

When open job count reaches the unit or namespace bound, the server SHALL carry limit as a quantity argument with target and targetKind as identifier arguments.

#### Scenario: Open job bound
- **WHEN** open job count reaches the unit or namespace bound
- **THEN** the answer carries limit, target and targetKind in errorArgs.

### Requirement: Job frozen

When a job start meets a freeze, the server SHALL carry freeze as an identifier argument.

#### Scenario: Freeze refusal
- **WHEN** a job start meets a freeze
- **THEN** the answer carries freeze in errorArgs.

### Requirement: Job tool unsupported

When a job start names a tool with no steps, the server SHALL carry tool as an identifier argument.

#### Scenario: Tool without steps
- **WHEN** a job start names a tool with no steps
- **THEN** the answer carries the NEW code job.tool-unsupported with tool in errorArgs.

### Requirement: Job executor missing

When a job start names an executor kind that is not installed, the server SHALL carry executor as an identifier argument.

#### Scenario: Missing executor
- **WHEN** a job start names an executor kind that is not installed
- **THEN** the answer carries the NEW code job.executor-missing with executor in errorArgs.
