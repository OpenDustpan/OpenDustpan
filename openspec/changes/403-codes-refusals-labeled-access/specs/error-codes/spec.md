## ADDED Requirements

### Requirement: job-token refusals

When the job start at pkg/job/service_start.go refuses a job token for a reason other than a missing grant, the server SHALL answer 403 with the NEW code job.token-refused.

#### Scenario: Job token refused at start
- **WHEN** the job start at pkg/job/service_start.go refuses a job token for a reason other than a missing grant
- **THEN** the server answers 403 with the NEW job.token-refused code, not access.denied.

### Requirement: connection refusals

When the job use at pkg/dustpan/jobservice.go refuses an unusable connection, the server SHALL answer 403 with the NEW code connection.unusable.

#### Scenario: Unusable connection refused
- **WHEN** the job use at pkg/dustpan/jobservice.go refuses an unusable connection
- **THEN** the server answers 403 with the NEW connection.unusable code, not access.denied.

### Requirement: state guard refusals

When the state guard at pkg/dustpan/stateguard.go refuses the subject for a reason other than a missing grant, the server SHALL answer 403 with the NEW code state.subject-refused.

#### Scenario: State guard subject refused
- **WHEN** the state guard at pkg/dustpan/stateguard.go refuses the subject for a reason other than a missing grant
- **THEN** the server answers 403 with the NEW state.subject-refused code, not access.denied.

### Requirement: gate store errors

When the store read fails inside gate CheckAccess, the server SHALL deny the call with status 503 and the NEW code gate.store-unavailable.

#### Scenario: Store error inside CheckAccess
- **WHEN** the store read fails inside gate CheckAccess
- **THEN** the server denies the call and answers 503 with the NEW gate.store-unavailable code, not access.denied.

### Requirement: gate store audit

When the store read fails inside gate CheckAccess, the server SHALL write a denial entry to the audit log.

#### Scenario: Store fault denial is recorded
- **WHEN** the store read fails inside gate CheckAccess
- **THEN** the audit log holds a denial entry for the call that records the store error.

### Requirement: true denials keep label

When a caller lacks the grant for the action, the server SHALL answer 403 with access.denied.

#### Scenario: Missing permission keeps access.denied
- **WHEN** a caller lacks the grant for the action
- **THEN** the server answers 403 with access.denied.

### Requirement: denied sentinels still deny

When a caller tests a NEW refusal error with errors.Is for identity.ErrDenied, the server error SHALL still report denied.

#### Scenario: Errors Is check still denies
- **WHEN** a caller tests a NEW refusal error with errors.Is for identity.ErrDenied
- **THEN** the caller is still denied.

### Requirement: mapper order

When an error wraps both a NEW refusal sentinel and a bare ErrDenied, the server SHALL select the NEW specific code before access.denied.

#### Scenario: Mixed chain keeps the specific code
- **WHEN** an error wraps both a NEW refusal sentinel and a bare ErrDenied
- **THEN** the server answers with the NEW specific code, not access.denied.

### Requirement: tool status unchanged

When a Pulumi CLI or OpenTofu call hits a relabeled job token, connection, or state guard refusal, the server SHALL keep the same HTTP status as on main.

#### Scenario: Tool sees the same status
- **WHEN** a Pulumi CLI or OpenTofu call hits a relabeled job token, connection, or state guard refusal
- **THEN** the tool endpoint answers the same HTTP status as on main with the NEW code only in the code member.

### Requirement: forbidden order fix

When the forbidden ordering test runs, the test suite SHALL pass without the unreachable csrf-before-other-site order pin.

#### Scenario: Unreachable order is removed
- **WHEN** the forbidden ordering test runs
- **THEN** the test passes without the csrf-before-other-site order pin.

### Requirement: docs wrap fix

When a reader opens the forbidden-error-codes page, the page SHALL wrap lines 53-54 like the rest of the page.

#### Scenario: Reference page wraps each entry
- **WHEN** a reader opens the forbidden-error-codes page
- **THEN** lines 53-54 wrap like the rest of the page.
