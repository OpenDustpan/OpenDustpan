## ADDED Requirements

### Requirement: Services stay out of the SDK

Where a Go program imports pkg/setting, pkg/collection or pkg/job, the server SHALL expose no service constructor in them.

#### Scenario: No service in pkg
- **WHEN** a Go program imports pkg/setting, pkg/collection and pkg/job
- **THEN** the program finds no Service, PlacementService or Reconciler constructor

### Requirement: Default collection kept

When a namespace is created, the server SHALL store a default collection and a catch-all placement rule in the create transaction.

#### Scenario: Create keeps defaults
- **WHEN** an administrator creates a namespace
- **THEN** the namespace has the default collection and the catch-all placement rule

### Requirement: Wiring kept

When the server starts, the server SHALL build the setting, collection placement and job reconcile services from the internal packages.

#### Scenario: Settings route works
- **WHEN** a client sends GET /api/dustpan/v1/namespaces/{namespace}/settings with an access token
- **THEN** the server answers as before the move
