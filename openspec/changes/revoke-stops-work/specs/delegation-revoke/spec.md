## ADDED Requirements

### Requirement: ended-delegation-refuses-the-next-request

When a delegation has ended, the server SHALL refuse each later request under that delegation and SHALL write one refusal entry that names the agent, the delegation and the cause.

#### Scenario: request-after-end
- **WHEN** the agent sends a request under a delegation that ended by revoke, expiry or any other cause
- **THEN** the server answers 403 with the NEW error code `delegation.ended`, names the delegation ID and the cause, and writes one refusal entry

#### Scenario: replay-of-an-allow-answer
- **WHEN** the agent replays the bytes of an earlier allow answer after the end
- **THEN** the server refuses the request with `delegation.ended`

#### Scenario: store-error-fails-closed
- **WHEN** the store returns an error while the server resolves the delegation
- **THEN** the server refuses the request, and during a database gap it answers 503 with `Retry-After: 5`

### Requirement: delegated-work-stops-with-delegation-ended

When a delegation ends, the server SHALL stop the work of that delegation with the stop reason `delegation-ended` at the next resolve.

#### Scenario: waiting-job
- **WHEN** a job of the delegation waits or has not started a phase that changes
- **THEN** the server cancels it with the stop reason `delegation-ended`

#### Scenario: trigger-and-schedule
- **WHEN** a run trigger, a drift schedule or a scheduled change belongs to the ended delegation
- **THEN** the server disables it with the stop reason `delegation-ended` and starts no job from it

#### Scenario: job-from-a-write-of-an-ended-delegation
- **WHEN** a trigger of any owner fires on a write that an ended delegation made
- **THEN** the job does not start and carries the stop reason `delegation-ended`

#### Scenario: run-of-an-ended-delegation
- **WHEN** a Dusty run names an ended delegation
- **THEN** the run ends at its next step or before its next tool call with the stop reason `delegation-ended`

### Requirement: running-phase-follows-the-setting

When a delegation ends while a job has a running phase, the server SHALL apply the installation setting of revocation during an apply, and SHALL show its effective value in the API.

#### Scenario: confirmed-phase-finishes
- **WHEN** the setting has its recommended value and a phase that the person confirmed is running
- **THEN** the phase finishes and no new phase starts

#### Scenario: unconfirmed-phase-stops
- **WHEN** the setting has its recommended value and a phase that the person did not confirm is running
- **THEN** the phase gets the refusal at its next call with the job token

#### Scenario: effective-value-in-the-api
- **WHEN** a person reads the setting
- **THEN** the answer shows the effective value and where it comes from

### Requirement: finished-results-stand

When a delegation ends, the server SHALL keep each tool result and each change that finished before the end.

#### Scenario: finished-call-stands
- **WHEN** the agent finished a tool call before the end
- **THEN** the result stands and the server undoes nothing that a job applied

### Requirement: agent-cannot-revoke

The server SHALL refuse a revoke request that comes from an agent, and SHALL write a denial entry.

#### Scenario: agent-revoke-refused
- **WHEN** an agent principal sends a revoke request for any delegation
- **THEN** the server refuses it, changes no row and writes one denial entry
