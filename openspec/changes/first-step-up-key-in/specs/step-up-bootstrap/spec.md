## ADDED Requirements

### Requirement: First key

When no marker is set and no administrator holds a key, the server SHALL enroll the first step-up key of the first administrator through the host command.

#### Scenario: First run
- **WHEN** the operator runs the bootstrap with a valid reason
- **THEN** the key exists, the marker is set and the audit entry is written

#### Scenario: Key from another route
- **WHEN** an administrator already holds a key and the operator runs the bootstrap
- **THEN** the command refuses and writes no key

### Requirement: Recovery mode

Where the recovery mode is bootstrap or signed-request, the server SHALL accept the host bootstrap.

#### Scenario: Mode 1
- **WHEN** the operator runs the bootstrap in recovery mode bootstrap
- **THEN** the key exists

#### Scenario: Mode 2
- **WHEN** the operator runs the bootstrap in recovery mode signed-request
- **THEN** the key exists

### Requirement: One transaction

The server SHALL write the key, the marker and the audit entry in one store transaction.

#### Scenario: Crash before commit
- **WHEN** the process stops before the commit and restarts
- **THEN** no key, no marker and no audit entry exist

### Requirement: Marker

If the marker is set, then the server SHALL refuse the host bootstrap.

#### Scenario: Second run
- **WHEN** the operator runs the bootstrap again
- **THEN** the command refuses, names the marker and writes no key

#### Scenario: Concurrent runs
- **WHEN** two bootstraps run at the same time
- **THEN** one key exists

### Requirement: Marker read

When a caller with the installation read action asks for the installation facts, the server SHALL return the marker.

#### Scenario: Read after bootstrap
- **WHEN** an authorized caller reads the installation facts after the bootstrap
- **THEN** the marker shows as set

#### Scenario: Read without the action
- **WHEN** a caller without the action reads the installation facts
- **THEN** the server answers 403 and shows no marker

### Requirement: Mode none

If the recovery mode is none, then the server SHALL refuse the host bootstrap.

#### Scenario: Mode none
- **WHEN** the operator runs the bootstrap in mode none
- **THEN** the error names the mode and the setting

### Requirement: Off switch

If the off-switch variable has a value other than true, 1, false or 0, then the server SHALL refuse the host bootstrap.

#### Scenario: Unknown value
- **WHEN** the variable is set to yes
- **THEN** the command refuses and writes no key

### Requirement: Pending migration

If the database has a pending migration, then the server SHALL refuse the host bootstrap.

#### Scenario: Pending migration
- **WHEN** the operator runs the bootstrap
- **THEN** the command refuses and names the command that applies migrations

### Requirement: Reason text

If the reason is missing or has control or format characters, then the server SHALL refuse the host bootstrap.

#### Scenario: Direction override
- **WHEN** the reason has a direction override
- **THEN** the command refuses

#### Scenario: Control character
- **WHEN** the reason has a control character
- **THEN** the command refuses

#### Scenario: No reason
- **WHEN** no reason is given
- **THEN** the command refuses

### Requirement: Email

When the key commit succeeds, the server SHALL send the email after the commit.

#### Scenario: Send fails
- **WHEN** the email send fails
- **THEN** the key stays and the server logs the failure

#### Scenario: Crash before send
- **WHEN** the process stops after the commit and before the send
- **THEN** the audit entry shows that the email is due

### Requirement: Challenge once

If a key-add challenge was used, then the server SHALL refuse a second use of it.

#### Scenario: Replay after restart
- **WHEN** a used challenge comes again after a restart
- **THEN** the server refuses it

#### Scenario: Concurrent use
- **WHEN** the admin path and the person path use one challenge at the same time
- **THEN** one use succeeds and the other is refused

### Requirement: Admin challenge count

While an administrator adds a key for a person, the server SHALL count the challenge apart from the challenge limit of the person.

#### Scenario: Admin challenges
- **WHEN** an administrator starts challenges up to the limit of the person
- **THEN** the person can still start a challenge

### Requirement: Admin reset

When a caller with the admin reset action resets the keys of a person, the server SHALL clear them through the store method for the reset and write an audit entry.

#### Scenario: Sole suspended key
- **WHEN** the administrator resets the sole suspended key
- **THEN** the person can enroll a new key and the audit entry exists

#### Scenario: Other keys
- **WHEN** the person holds more than one key and the administrator resets them
- **THEN** all keys are cleared and the audit entry exists

#### Scenario: Not an administrator
- **WHEN** a caller without the action asks for the reset
- **THEN** the server answers 403 and clears no key
