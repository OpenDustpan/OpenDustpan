## ADDED Requirements

### Requirement: Commit config edit

When a principal edits a config value of a unit through the API, the server SHALL commit the edit to the forge with one trailer that holds the job ID.

#### Scenario: API edit commits with job trailer
- **WHEN** a principal with the write grant edits a config value of a unit through the API
- **THEN** the server writes one commit on the forge with the trailer Dustpan-Job holding the job ID, and the next plan shows the edited value

### Requirement: Commit trailer text

When the deliver phase writes the commit, the server SHALL end the commit message with one line of the form Dustpan-Job: <job ID>.

#### Scenario: Trailer key exact
- **WHEN** the deliver phase writes the commit
- **THEN** the commit message ends with one line Dustpan-Job: <job ID> and names the starting principal in the body

### Requirement: Apply only

When a write-back job names an operation other than apply, the server SHALL fail the job with ErrUnsupported.

#### Scenario: Plan operation refused
- **WHEN** a write-back job names the operation plan
- **THEN** the server fails the job with ErrUnsupported and writes no commit

### Requirement: Refuse non-config edit

When an edit names a path that the StepPlanner of the tool marks as not editable, the server SHALL refuse the edit without a forge call.

#### Scenario: Edit outside config refused
- **WHEN** an edit names a path that the StepPlanner of the tool marks as not editable
- **THEN** the server refuses the edit without a forge call

### Requirement: Stale base fails

When a key of the edits changed on the forge after the base commit, the server SHALL fail the job and name the changed key.

#### Scenario: Changed key after base fails job
- **WHEN** a key of the edits changed on the forge after the base commit
- **THEN** the server fails the job and names the changed key

### Requirement: Forge refused

If the forge answers with 4xx, then the server SHALL fail the job without retry.

#### Scenario: Refused error fails
- **WHEN** the forge answers with 4xx
- **THEN** the server fails the job without retry

### Requirement: Forge retryable bounded

When the forge answers with a transport error, a timeout, 429, or 5xx, the server SHALL retry the commit with backoff and fail the job after 5 attempts.

#### Scenario: Retryable error retries with bound
- **WHEN** the forge answers with a transport error, a timeout, 429, or 5xx
- **THEN** the server retries the commit with backoff and fails the job after 5 attempts

### Requirement: Single commit on retry

When the server retries the deliver phase after a crash, the server SHALL write at most 1 commit on the forge for the job.

#### Scenario: Crash between commit and record makes no duplicate
- **WHEN** the server crashes after the forge commit and before the job record names the commit
- **THEN** a retry of the deliver phase records the same commit and writes no second commit
