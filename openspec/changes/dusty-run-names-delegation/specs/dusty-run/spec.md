## ADDED Requirements

### Requirement: A run names a delegation

When a person starts a Dusty run, the server SHALL store the ID of one delegation of that person to Dusty with the run, and SHALL refuse a start that names no valid delegation.

#### Scenario: Start under a delegation
- **WHEN** a person in a console session starts a run and names a live delegation to Dusty that covers the namespace
- **THEN** the run holds the delegation ID, the person and the credential ID.

#### Scenario: No delegation
- **WHEN** a person starts a run with no delegation ID
- **THEN** the start fails with the code agent.delegation-required and no run is written.

#### Scenario: Delegation that does not fit
- **WHEN** a person starts a run with a delegation that is unknown, ended, of another person, for another agent or outside the namespace
- **THEN** the start fails with the code agent.delegation-invalid and no run is written.

### Requirement: No start from a delegated token

If a request that starts a run carries a delegated token, then the server SHALL refuse it.

#### Scenario: Delegated token
- **WHEN** a delegated token starts a run
- **THEN** the start fails with the code agent.delegated-token-refused and no run is written.

### Requirement: The run ends with its delegation

While a run is active, the server SHALL resolve its delegation at the start of each step and before each tool call, and SHALL end the run with the stop reason delegation-ended when the delegation ended.

#### Scenario: Revoke between steps
- **WHEN** the person revokes the delegation after the model step and before the tools step
- **THEN** no tool call runs and the run ends with the stop reason delegation-ended.

#### Scenario: Revoke while a call waits
- **WHEN** the delegation ends after a turn started and another instance takes the turn over
- **THEN** the next step resolves the delegation again and the run ends with delegation-ended.

### Requirement: Both parts allow a tool call

The server SHALL run a tool call of a Dusty run only when the grants of the person allow it and the expanded scope of the delegation lists the action on the object.

#### Scenario: Outside the scope
- **WHEN** Dusty calls a tool whose action the delegation does not list
- **THEN** the call fails, no tool runs, and a denied entry names the cause delegation-scope.

#### Scenario: Person has no grant
- **WHEN** the delegation lists the action and the person has lost the grant
- **THEN** the call fails with the existing authorization error.

### Requirement: The audit entry names the delegation

Each tool call of a run SHALL write an entry whose agent block holds the delegation ID, and whose subject is the person.

#### Scenario: Entry of a call
- **WHEN** a run calls a read tool
- **THEN** one entry exists with the person as subject, the delegation ID and the credential kind agent-run.

### Requirement: A stored run

The server SHALL keep each run in the store, with its delegation ID, and the store adapters SHALL pass the run store conformance suite.

#### Scenario: Restart
- **WHEN** an instance stops during a turn and another instance takes the turn over
- **THEN** the run continues from the last recorded step with the same delegation ID.
