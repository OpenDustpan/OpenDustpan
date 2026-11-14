## ADDED Requirements

### Requirement: List pools

When a principal with namespace:read-runners lists pools, the server SHALL return each pool of the namespace with no join secret.

#### Scenario: List pools
- **WHEN** a principal with namespace:read-runners lists the pools of a namespace
- **THEN** the server returns each pool with status and release and no join secret

#### Scenario: No credential
- **WHEN** a caller with no credential requests a pool route
- **THEN** the server answers 401 and returns no pool data

#### Scenario: No grant
- **WHEN** a principal without namespace:read-runners requests a pool route
- **THEN** the server answers 403 and returns no pool data

### Requirement: Create pool

When a principal with namespace:manage-runners creates a pool, the server SHALL store the pool and one pool-created audit entry in one transaction.

#### Scenario: Create
- **WHEN** a principal with namespace:manage-runners creates a pool
- **THEN** the server stores the pool and one audit entry together

#### Scenario: Audit fails
- **WHEN** the audit write fails
- **THEN** the server stores no pool row and no audit entry

#### Scenario: Duplicate name
- **WHEN** a principal creates a pool with a name that exists
- **THEN** the server answers 409 and stores no row

### Requirement: Change and delete pools

When a principal with namespace:manage-runners changes or deletes a pool, the server SHALL store the change and its audit entry in one transaction.

#### Scenario: Delete
- **WHEN** a principal deletes a pool with no live runner and no active token
- **THEN** the server deletes the pool and stores one pool-deleted audit entry

#### Scenario: Delete in use
- **WHEN** a principal deletes a pool with a live runner
- **THEN** the server keeps the pool and answers 409

### Requirement: Drain and retire

When a principal with namespace:manage-runners drains or retires a runner, the server SHALL store the change and its audit entry in one transaction.

#### Scenario: Drain
- **WHEN** a principal drains a runner
- **THEN** the server marks it draining and stores one runner-drained entry

#### Scenario: Retire
- **WHEN** a principal retires a runner
- **THEN** the server retires it and stores one runner-retired entry

### Requirement: Join tokens

When a principal with namespace:manage-runners creates a join token, the server SHALL return the secret once at create and never on a later read or list.

#### Scenario: Create
- **WHEN** a principal creates a join token
- **THEN** the answer holds the secret once and a list holds none

#### Scenario: Revoke
- **WHEN** a principal revokes a join token
- **THEN** the server revokes it and stores one join-token-revoked entry

### Requirement: No alias

When a caller requests an old pool path, the server SHALL answer 404 with no redirect and no pool data.

#### Scenario: Old path
- **WHEN** a caller requests the old pool path
- **THEN** the server answers 404 and names the new path
