## ADDED Requirements

### Requirement: agent-principal

When an administrator connects a model, the server SHALL make one agent principal with stable ID dusty of the NEW kind agent.

#### Scenario: dusty principal exists
- **WHEN** an administrator connects a model
- **THEN** the server returns one principal of the NEW kind agent with stable ID dusty

### Requirement: create-delegation

When a person sends CreateDelegation for dusty, the server SHALL store one delegation record with holder dusty, scope, creator and time.

#### Scenario: person creates delegation
- **WHEN** a person with namespace read on the scope namespace sends CreateDelegation for dusty with level look, suggest or operate and expiry within 30 days
- **THEN** the server stores one row with holder dusty, creator equal to caller, scope namespace id, level, creator and time and returns its id with prefix dlg-

### Requirement: create-delegation-refuses-bad-input

When CreateDelegation carries an invalid level, scope, expiry or caller right, the server SHALL refuse the write with 422 or 403 and store no row.

#### Scenario: bad level refused
- **WHEN** the caller sends level admin
- **THEN** the server answers 422 with the NEW error code delegation.invalid-level and stores no row

#### Scenario: unknown namespace refused
- **WHEN** the caller sends a scope namespace id that does not exist
- **THEN** the server answers 422 with the NEW error code delegation.unknown-namespace and stores no row

#### Scenario: bad expiry refused
- **WHEN** the caller sends an expiry in the past or more than 30 days after creation
- **THEN** the server answers 422 with the NEW error code delegation.invalid-expiry and stores no row

#### Scenario: caller without membership refused
- **WHEN** a caller without namespace read on the scope namespace sends CreateDelegation
- **THEN** the server answers 403 with the NEW error code delegation.forbidden and stores no row

### Requirement: list-own-delegations

When a person sends ListDelegations, the server SHALL return each delegation that names the person as creator.

#### Scenario: creator lists own rows
- **WHEN** the creator sends ListDelegations
- **THEN** the server returns the one row with holder dusty, live state, level, scope namespace id and expiry

#### Scenario: empty list
- **WHEN** a person with no delegation sends ListDelegations
- **THEN** the server answers 200 with an empty list

#### Scenario: expired row stays visible
- **WHEN** the creator lists after the expiry time
- **THEN** the server returns the row with live state expired

### Requirement: read-one-delegation

When a caller reads one delegation id, the server SHALL return the row to its creator or to an administrator with scope manage and answer 404 otherwise.

#### Scenario: creator reads own row
- **WHEN** the creator reads an own delegation id
- **THEN** the server returns the row with holder dusty, scope, creator, time, level and live state

#### Scenario: unknown id
- **WHEN** any caller reads an unknown delegation id
- **THEN** the server answers 404 with the NEW error code delegation.not-found

#### Scenario: other creator hidden
- **WHEN** a plain member reads a delegation id of another creator
- **THEN** the server answers 404 with the NEW error code delegation.not-found

### Requirement: list-by-namespace

When a caller with namespace manage sends the namespace delegation list, the server SHALL return each delegation in that namespace.

#### Scenario: administrator lists namespace rows
- **WHEN** a caller with namespace manage on the namespace sends the namespace list
- **THEN** the server returns each delegation whose scope is that namespace id

#### Scenario: member refused
- **WHEN** a caller without namespace manage sends the namespace list
- **THEN** the server answers 403 with the NEW error code delegation.forbidden

### Requirement: revoke-delegation

When an allowed revoker with a live credential sends RevokeDelegation, the server SHALL mark the delegation revoked.

#### Scenario: creator revokes own row
- **WHEN** the creator with a live credential sends RevokeDelegation with no extra fresh step
- **THEN** the server marks the row revoked and writes an audit entry that names holder dusty, creator and revoker

#### Scenario: emergency revoke by administrator
- **WHEN** a caller with namespace manage on the scope sends RevokeDelegation for a row of another creator with a live credential and no extra fresh step
- **THEN** the server marks the row revoked and writes an audit entry that names the revoker

#### Scenario: second revoke is idempotent
- **WHEN** any allowed revoker revokes an already revoked row
- **THEN** the server answers success with no further state change

#### Scenario: unknown id on revoke
- **WHEN** a caller revokes an unknown delegation id
- **THEN** the server answers 404 with the NEW error code delegation.not-found
