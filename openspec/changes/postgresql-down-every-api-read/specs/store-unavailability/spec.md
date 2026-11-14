## ADDED Requirements

### Requirement: unavailable-read-answer

When the store reports database unavailability, the server SHALL answer the read with error code `namespace.unavailable` through NEW mapStoreError.

#### Scenario: maintenance read while PostgreSQL is down
- **WHEN** PostgreSQL is down and GET /api/dustpan/v1/maintenance arrives
- **THEN** the server answers 503 namespace.unavailable with Retry-After 5 seconds, not maintenance mode, through NEW mapStoreError

#### Scenario: failed mode read
- **WHEN** the installation mode read fails from database unavailability
- **THEN** the server answers 503 namespace.unavailable through NEW mapStoreError

#### Scenario: inventory read
- **WHEN** a read arrives at ANY /api/inventory/unit-collection while PostgreSQL is down
- **THEN** the server answers 503 namespace.unavailable through NEW mapStoreError

#### Scenario: recovery
- **WHEN** PostgreSQL restarts after the outage
- **THEN** reads succeed in normal mode without a server restart

### Requirement: real-maintenance-preserved

While the installation holds maintenance mode, the server SHALL answer reads with error code `maintenance.in-maintenance`.

#### Scenario: real maintenance
- **WHEN** the installation holds maintenance mode and the store is reachable
- **THEN** the server answers maintenance.in-maintenance

### Requirement: cause-class

When an error wraps a store connection failure, the server SHALL return database-unavailable through NEW CauseClass without driver detail.

#### Scenario: wrapped forms
- **WHEN** CauseClass gets a timeout, a refused connection, an exhausted pool or a triply wrapped error
- **THEN** NEW CauseClass returns database-unavailable

### Requirement: tool-contract-unavailable

When the store reports database unavailability on a tool state read, the server SHALL answer 503 in the tool contract shape.

#### Scenario: OpenTofu state read
- **WHEN** an OpenTofu state read arrives while PostgreSQL is down
- **THEN** the TFE adapter answers 503 in the JSON:API error shape through NEW mapStoreError

#### Scenario: Pulumi state read
- **WHEN** a Pulumi state read arrives while PostgreSQL is down
- **THEN** the Pulumi adapter answers 503 in the code and message shape through NEW mapStoreError

### Requirement: worker-cause-class

When a durable claim or schedule call fails from database unavailability, the worker SHALL log the cause class through NEW logCauseClass without driver detail.

#### Scenario: claim failure
- **WHEN** a durable claim fails from database unavailability
- **THEN** the WARN line names database-unavailable through NEW logCauseClass without driver text

#### Scenario: hostile error
- **WHEN** the wrapped error message holds a host, DSN userinfo and credential
- **THEN** NEW logCauseClass omits each of them
