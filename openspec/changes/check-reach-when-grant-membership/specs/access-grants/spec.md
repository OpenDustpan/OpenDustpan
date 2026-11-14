## ADDED Requirements

### Requirement: Token-right grant needs cover

When a grant gives a role with principal:manage-tokens on a principal, the server SHALL refuse the grant unless the reach of the caller covers the reach of that principal.

#### Scenario: Non-covering grant refused
- **WHEN** the caller reach does not cover the target reach
- **THEN** the server answers 403, names the target and the missing actions, and stores no grant

#### Scenario: Covering group grant allowed
- **WHEN** the caller reach covers the target reach for a group grant
- **THEN** the server stores the grant with its approved reach

#### Scenario: Concurrent grant
- **WHEN** two grants run at the same time and one changes the reach that the other needs
- **THEN** the server refuses the grant that no longer has cover

### Requirement: Protecting grant needs a trust decision

If a grant makes a group protected while the group has members from the identity provider, then the server SHALL refuse the grant unless the same request decides the trust of the group.

#### Scenario: No identity provider members
- **WHEN** the group has no member with source scim or claim
- **THEN** the server stores the grant

#### Scenario: Members and no decision
- **WHEN** the group has such members and the request has no trust decision
- **THEN** the server answers 409, names the group and the member count, and stores no grant

#### Scenario: Members and a decision
- **WHEN** the request decides trust
- **THEN** the server stores the grant and the trust setting in one transaction

### Requirement: Widened edge is flagged

When a write widens the reach of an act-as edge beyond its approved reach, the server SHALL keep the write and flag the edge.

#### Scenario: Grant growth
- **WHEN** a grant to the target adds an unapproved action
- **THEN** the server keeps the grant and lists the flag

#### Scenario: Member add growth
- **WHEN** a member add gives a group target more reach than approved
- **THEN** the server adds the member and flags the edge

#### Scenario: Role change growth
- **WHEN** a role of the edge gets a new action
- **THEN** the server keeps the change and flags the edge

#### Scenario: Maker loses the path
- **WHEN** the reach of the granter shrinks after the grant
- **THEN** the server keeps the edge and flags it

#### Scenario: Regrowth after acknowledge
- **WHEN** an acknowledged edge grows again
- **THEN** the server sets a new flag and keeps the old one in the audit log

### Requirement: Flag is acknowledged by a covering person

When a caller whose reach covers a flagged edge acknowledges it, the server SHALL clear the flag and write an audit entry that names the caller and the edge.

#### Scenario: Acknowledge clears the flag
- **WHEN** a covering person acknowledges
- **THEN** the server clears it and writes an audit entry

#### Scenario: Revoke removes the edge
- **WHEN** a covering person revokes the edge
- **THEN** the server removes the grant and writes an audit entry

#### Scenario: Non-covering acknowledge refused
- **WHEN** a caller without cover acknowledges
- **THEN** the server answers 403 and the flag stays

#### Scenario: Cover lost before the action
- **WHEN** the caller loses cover after it read the flag
- **THEN** the server checks cover again, answers 403 and keeps the flag

#### Scenario: List by reach
- **WHEN** a caller lists flagged edges
- **THEN** the server lists only the edges that the caller reach covers

### Requirement: Flag ends with its edge

When the grant, the principal, the group or the membership of a flagged edge ends, the server SHALL end the flag in the same transaction and write an audit entry.

#### Scenario: Grant revoked
- **WHEN** a person revokes the grant by another path
- **THEN** the server ends the flag and writes an audit entry

#### Scenario: Principal deleted
- **WHEN** a person deletes the principal
- **THEN** the server ends the flag and writes an audit entry

#### Scenario: Group deleted
- **WHEN** a person deletes the group
- **THEN** the server ends the flag and writes an audit entry

#### Scenario: Member removed
- **WHEN** a person removes the member that made the edge
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

#### Scenario: Description text
- **WHEN** the role catalog is read
- **THEN** the description states the limit
