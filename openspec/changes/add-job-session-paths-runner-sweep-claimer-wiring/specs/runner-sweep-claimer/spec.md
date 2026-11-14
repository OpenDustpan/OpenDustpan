## ADDED Requirements

### Requirement: claim-sweeps-by-purpose

When a runner of a pool polls, the server SHALL claim only a durable operation whose kind matches the purpose of the pool.

#### Scenario: Sweep pool claims a sweep operation
- **WHEN** a runner of a pool with the sweep purpose polls while a sweep operation of that pool waits
- **THEN** the server answers 200 with sweep work that names no service account.

#### Scenario: Purpose mismatch gets no work
- **WHEN** a runner polls a pool whose purpose does not match the operation kind
- **THEN** the server answers 204 and leaves the operation queued.

#### Scenario: Purpose changes during a claim
- **WHEN** the purpose of a pool changes while a claim runs
- **THEN** the server reads the new purpose in the claim transaction and answers 204 for a kind that no longer matches.

### Requirement: sweep-report-auth

If a sweep report or finish request lacks a valid session proof, an open session or ownership of the claim, then the server SHALL refuse the request.

#### Scenario: Runner key proof
- **WHEN** a caller uploads a report chunk with a runner key proof and no session proof
- **THEN** the server answers 401 bad-proof and stores nothing.

#### Scenario: No open session
- **WHEN** a launcher uploads a report for a session that is not open
- **THEN** the server refuses the request and stores nothing.

### Requirement: bounded-sweep-report

While a sweep session uploads its report, the server SHALL enforce the bounds of the report and store the stream encrypted with the data key of the namespace.

#### Scenario: Over-bound line refused
- **WHEN** the launcher uploads a line above 64 KiB, or a report above 1 GiB or 5,000,000 records
- **THEN** the server answers 413, drops the staged report and fails the sweep.

#### Scenario: Tag and scope limits
- **WHEN** a record has more than 50 tags, a key above 128 bytes or a value above 256 bytes, or the header has more than 1,000 scopes
- **THEN** the server refuses the report, drops the staged report and fails the sweep.

#### Scenario: Valid report stored encrypted
- **WHEN** the launcher uploads a bounded report under its sweep session
- **THEN** the server stores the stream under the data key of the namespace.

#### Scenario: Cut stream
- **WHEN** the connection drops before finish
- **THEN** the server drops the staged report and fails the sweep when the claim ends.

#### Scenario: Second upload
- **WHEN** the launcher uploads a second report for the same sweep session
- **THEN** the server answers 409 and keeps the first staged report.

#### Scenario: Scope outside the target
- **WHEN** the header of the report names a scope that the target does not name
- **THEN** the server answers 400, drops the staged report and fails the sweep.

### Requirement: valid-sweep-report-format

If a sweep report has no header line, a wrong format or version, or a malformed line, then the server SHALL refuse the report.

#### Scenario: No header
- **WHEN** the first line of the report is not a header
- **THEN** the server answers 400 and fails the sweep.

#### Scenario: Malformed line
- **WHEN** a record line is not valid JSON
- **THEN** the server answers 400, drops the staged report and fails the sweep.

#### Scenario: Incomplete scope
- **WHEN** the header marks a scope with complete false
- **THEN** the server accepts the report and closes no finding of that scope.

### Requirement: sweep-finish-session

When the launcher calls the sweep finish route, the server SHALL close the session and advance the sweep only after a complete bounded report is stored.

#### Scenario: Finish closes the session
- **WHEN** the launcher calls finish after a complete bounded upload
- **THEN** the server closes the session and advances the sweep from running to matching.

#### Scenario: Finish with no report
- **WHEN** the launcher calls finish with no stored report
- **THEN** the server answers 400 and fails the sweep.

#### Scenario: Finish twice
- **WHEN** the launcher calls finish a second time, or two finish calls arrive at once
- **THEN** the server answers the stored result and changes nothing.

#### Scenario: Finish with a lost claim
- **WHEN** the launcher calls finish after its claim expired
- **THEN** the server answers 409 and keeps the sweep state.

### Requirement: refuse-sweep-namespace-pool

When a caller creates or updates a namespace pool with the sweep purpose, the server SHALL refuse the write.

#### Scenario: Namespace pool refused
- **WHEN** a caller creates or updates a namespace pool with the sweep purpose
- **THEN** the server answers 422 and stores no change.

#### Scenario: Installation pool accepted
- **WHEN** an operator creates an installation pool with the sweep purpose
- **THEN** the server stores the pool.

#### Scenario: Purpose change with open operations
- **WHEN** a caller changes the purpose of a pool that has queued or claimed operations of the old purpose
- **THEN** the server answers 422 and keeps the purpose.
