## ADDED Requirements

### Requirement: Create freeze

When a principal with the freeze manage action creates a freeze on a namespace with no active freeze and a reason of 1 to 500 characters, the server SHALL store the freeze.

#### Scenario: Create freeze blocks job start
- **WHEN** a principal with the freeze manage action creates a freeze on a namespace with no active freeze and a reason of 1 to 500 characters
- **THEN** the server stores the freeze with creator, reason, and time, and writes an audit entry that names the creator and the reason

#### Scenario: Create with long reason is refused
- **WHEN** a principal creates a freeze with a reason longer than 500 characters
- **THEN** the server answers 400 and stores no freeze

### Requirement: Refuse second create

While a namespace has an active freeze, the server SHALL refuse a second freeze create.

#### Scenario: Second create is refused
- **WHEN** a principal creates a freeze on a namespace with an active freeze
- **THEN** the server answers 409 and keeps the first freeze unchanged

### Requirement: End active freeze

When a principal with the freeze manage action ends the active freeze of a namespace, the server SHALL mark the freeze ended.

#### Scenario: End freeze permits job start
- **WHEN** a principal with the freeze manage action ends the active freeze
- **THEN** the server records the end with end time and actor, writes an audit entry for the end, and permits the next job start

### Requirement: Refuse end with none active

When a principal ends a freeze on a namespace with no active freeze, the server SHALL refuse the end.

#### Scenario: End with none active is refused
- **WHEN** a principal ends a freeze on a namespace with no active freeze
- **THEN** the server answers 404 and changes nothing

### Requirement: Refuse job start during freeze

While a namespace has an active freeze, the server SHALL refuse a job start without the break-freeze action.

#### Scenario: Start during freeze is refused
- **WHEN** a principal without the break-freeze action starts a job in a namespace with an active freeze
- **THEN** the server answers 403 and starts no job

### Requirement: Permit break-glass start

When a principal with the break-freeze action starts a job during an active freeze with a reason of 1 to 500 characters, the server SHALL start the job.

#### Scenario: Break-glass start is recorded
- **WHEN** a principal with the break-freeze action starts a job in a namespace with an active freeze and a reason of 1 to 500 characters
- **THEN** the server starts the job and writes an audit entry that names the freeze, the starter, and the reason

#### Scenario: Break-glass start without reason is refused
- **WHEN** a principal with the break-freeze action starts a job during an active freeze with an empty reason
- **THEN** the server answers 400 and starts no job

### Requirement: List freezes

When a principal with the namespace read action lists the freezes of a namespace, the server SHALL return the active freeze and each ended freeze.

#### Scenario: List shows active and ended freezes
- **WHEN** a principal with the namespace read action lists the freezes of a namespace
- **THEN** the server returns the active freeze and each ended freeze in time order with reason, creator, and times
