## ADDED Requirements

### Requirement: List and read pools

When a principal with namespace:read-runners lists pools, the server SHALL return each pool of the namespace with no join secret.

#### Scenario: List pools in a namespace
- **WHEN** A principal with namespace:read-runners lists the pools of a namespace
- **THEN** The server returns each pool of the namespace with its status and release and no join secret.

#### Scenario: No credential gets 401
- **WHEN** A caller with no credential requests a pool route
- **THEN** The server answers 401 and returns no pool data.

#### Scenario: No grant gets 403
- **WHEN** A principal without namespace:read-runners requests a pool route
- **THEN** The server answers 403 and returns no pool data.

### Requirement: Create and change a pool

When a principal with namespace:manage-runners creates, changes, or deletes a pool, the server SHALL store the change and its audit entry in one transaction.

#### Scenario: Create pool writes audit
- **WHEN** A principal with namespace:manage-runners creates a pool
- **THEN** The server stores the pool and one pool-created audit entry in the same transaction.

#### Scenario: Change pool with runners
- **WHEN** A principal with namespace:manage-runners changes a pool that still holds runners
- **THEN** The server stores the change and one pool-changed audit entry in the same transaction.

#### Scenario: Delete pool writes audit
- **WHEN** A principal with namespace:manage-runners deletes a pool
- **THEN** The server deletes the pool and stores one pool-deleted audit entry in the same transaction.

### Requirement: Drain and retire runners

When a principal with namespace:manage-runners drains or retires a runner, the server SHALL store the change and its audit entry in one transaction.

#### Scenario: Drain runner writes audit
- **WHEN** A principal with namespace:manage-runners drains a runner
- **THEN** The server marks the runner draining and stores one runner-drained audit entry in the same transaction.

#### Scenario: Retire runner writes audit
- **WHEN** A principal with namespace:manage-runners retires a runner
- **THEN** The server retires the runner and stores one runner-retired audit entry in the same transaction.

### Requirement: Join token create and revoke

When a principal with namespace:manage-runners creates or revokes a join token, the server SHALL store the change and its audit entry in one transaction.

#### Scenario: Create token hides secret on read
- **WHEN** A principal with namespace:manage-runners creates a join token
- **THEN** The server returns the secret once at create and never on a later read or list.

#### Scenario: Failed create leaks no secret
- **WHEN** A join token create fails before commit
- **THEN** The server stores no token row and returns no secret.

#### Scenario: Revoke writes audit
- **WHEN** A principal with namespace:manage-runners revokes a join token
- **THEN** The server revokes the token and stores one join-token-revoked audit entry in the same transaction.

#### Scenario: Failed audit rolls back
- **WHEN** The audit write of a token revoke fails
- **THEN** The server keeps the token live and stores no revoke change.

### Requirement: Stable IDs for pools and tokens

Where a pool or join token is stored, the server SHALL key the stored reference by its stable ID.

#### Scenario: Remade pool gets a new ID
- **WHEN** A principal removes a pool and makes a pool with the same name again
- **THEN** The server treats the new pool as a new object with no history of the old pool.

### Requirement: Old paths have no alias

Where the caller requests an old pool path, the server SHALL answer 404 with no redirect and no pool data.

#### Scenario: Old path is unknown
- **WHEN** A caller requests the old pool path
- **THEN** The server answers 404 with the new path.
