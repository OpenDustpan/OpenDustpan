## ADDED Requirements

### Requirement: Token-right grant needs cover

When a grant gives a role with principal:manage-tokens on a principal, the server SHALL refuse the grant unless the reach of the caller covers the reach of that principal at the time of the grant.

#### Scenario: Non-covering grant refused
- **WHEN** the caller reach does not cover the target reach
- **THEN** the server answers 403, names the target and the missing actions, and stores no grant

#### Scenario: Covering group grant allowed
- **WHEN** the caller reach covers the target reach for a group grant
- **THEN** the server stores the grant with its approved reach

#### Scenario: Concurrent grant
- **WHEN** two grants run at the same time and one changes the reach that the other needs
- **THEN** the server refuses the grant that no longer has cover

### Requirement: Widened edge is flagged

When the reach of a target grows beyond the reach that the granter approved, the server SHALL keep the change and flag the edge until a covering person acknowledges or revokes it.

#### Scenario: Growth keeps the edge
- **WHEN** a grant to the target adds an action that the granter did not approve
- **THEN** the server keeps the grant and lists the flag in the access-review export

#### Scenario: Maker loses the path
- **WHEN** the reach of the granter shrinks after the grant
- **THEN** the server keeps the edge and flags it

### Requirement: Flag is acknowledged by a covering person

When a caller whose reach covers the flagged edge acknowledges it, the server SHALL clear the flag and write an audit entry that names the caller and the edge.

#### Scenario: Acknowledge clears the flag
- **WHEN** a covering person acknowledges the flag
- **THEN** the server clears it and writes an audit entry

#### Scenario: Revoke removes the edge
- **WHEN** a covering person revokes the flagged edge
- **THEN** the server removes the grant and writes an audit entry

#### Scenario: Non-covering acknowledge refused
- **WHEN** a caller without cover acknowledges the flag
- **THEN** the server answers 403 and the flag stays

#### Scenario: Cover lost before the action
- **WHEN** the cover of the caller is lost after the caller read the flag
- **THEN** the server checks cover again in the write transaction, answers 403 and keeps the flag

### Requirement: Flag ends with its edge

When the grant, the principal or the group of a flagged edge ends, the server SHALL end the flag in the same transaction and write an audit entry.

#### Scenario: Grant revoked
- **WHEN** a person revokes the grant of a flagged edge by another path
- **THEN** the server ends the flag and writes an audit entry

#### Scenario: Principal deleted
- **WHEN** a person deletes the principal of a flagged edge
- **THEN** the server ends the flag and writes an audit entry

### Requirement: Flag routes declare actions

The server SHALL declare an authorization action and an object for each route that lists, acknowledges or revokes a flagged edge.

#### Scenario: Route without action fails
- **WHEN** the route test finds a flag route with no action
- **THEN** the route test fails

### Requirement: Role description states limit

The server SHALL describe account-administrator as making tokens only for service principals whose grants fit inside its own reach.

#### Scenario: Operator actions absent
- **WHEN** an account administrator is checked against a service principal with each operator-family role
- **THEN** the reach holds no operator-family action
