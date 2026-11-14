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
- **THEN** the commit message ends with one line Dustpan-Job: <job ID>

### Requirement: Refuse non-config edit

When an edit names a path that the StepPlanner of the tool marks as not editable, the server SHALL refuse the edit without a forge call.

#### Scenario: Edit outside config refused
- **WHEN** an edit names a path that the StepPlanner of the tool marks as not editable
- **THEN** the server refuses the edit without a forge call

### Requirement: Path containment

When an edit names a path that is not a clean relative path inside the code location, the server SHALL refuse the edit without a forge call.

#### Scenario: Absolute path refused
- **WHEN** an edit names an absolute path
- **THEN** the server refuses the edit without a forge call

#### Scenario: Traversal path refused
- **WHEN** an edit names a path with dot-dot that leaves the code location
- **THEN** the server refuses the edit without a forge call

### Requirement: Missing file creates

When an edit names an editable path that has no file at the base commit, the server SHALL create the file with only the keys of the edits.

#### Scenario: Edit on missing file creates it
- **WHEN** an edit names an editable path that has no file at the base commit
- **THEN** the server creates the file with only the keys of the edits

### Requirement: No editable files

When the StepPlanner of the tool marks no file as editable, the server SHALL refuse the edit without a forge call.

#### Scenario: Tool with empty editable set refused
- **WHEN** the StepPlanner of the tool marks no file as editable
- **THEN** the server refuses the edit without a forge call and names the empty editable set

### Requirement: Invalid edit value

When an edit removes a needed key or sets an invalid value, the server SHALL refuse the edit without a forge call.

#### Scenario: Removed key refused
- **WHEN** an edit removes a key that the tool schema needs
- **THEN** the server refuses the edit without a forge call and names the removed key

#### Scenario: Invalid value refused
- **WHEN** an edit sets a value that fails the tool validation
- **THEN** the server refuses the edit without a forge call and names the invalid value

### Requirement: Stale base fails

When a key of the edits changed on the forge after the base commit, the server SHALL fail the job and name the changed key.

#### Scenario: Changed key after base fails job
- **WHEN** a key of the edits changed on the forge after the base commit
- **THEN** the server fails the job and names the changed key

### Requirement: Base still head

When the base commit is no longer the head of the branch at commit time, the server SHALL fail the job without pushing a commit.

#### Scenario: Race with team push fails job
- **WHEN** the base commit is no longer the head of the branch at commit time
- **THEN** the server fails the job and names the moved branch head

### Requirement: Concurrent second edit

When two write-back jobs start from the same base commit and the first job commits first, the server SHALL fail the second job at commit time without pushing a commit.

#### Scenario: Second job on stale base fails at commit
- **WHEN** two write-back jobs start from the same base commit and the first job commits first
- **THEN** the second job fails without pushing a commit and names the moved branch head

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

### Requirement: Grant refusal

When a principal without the unit write grant edits a config value, the server SHALL answer 403 and write the denial to the audit log.

#### Scenario: Missing write grant refused
- **WHEN** a principal without the unit write grant edits a config value
- **THEN** the server answers 403 and writes the denial to the audit log

### Requirement: Secret masking

When a forge call fails, the server SHALL write the error class to the job log with no secret value.

#### Scenario: Forge secret masked in log
- **WHEN** a forge call fails and returns text that holds a secret
- **THEN** the job log holds the error class with no secret value

### Requirement: Single commit on retry

When the server retries the deliver phase after a crash, the server SHALL write at most one commit on the forge for the job.

#### Scenario: Crash between commit and record makes no duplicate
- **WHEN** the server crashes after the forge commit and before the job record names the commit
- **THEN** a retry of the deliver phase records the same commit and writes no second commit

#### Scenario: Restart with no record retries idempotently
- **WHEN** the server restarts before any job record exists for the commit
- **THEN** the restarted deliver phase finds the commit by job ID and writes no second commit

### Requirement: Job close

When a write-back job commits, the server SHALL close the job on the next final state version of the unit.

#### Scenario: Next state version closes job
- **WHEN** the team CI applies the committed edit after the merge
- **THEN** the job closes when the next final state version of the unit arrives

#### Scenario: Job expires
- **WHEN** no final state version of the unit arrives
- **THEN** the job expires after 14 days by default
