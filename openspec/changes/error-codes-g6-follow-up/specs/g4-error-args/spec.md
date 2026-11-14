## ADDED Requirements

### Requirement: Collection reference

When a collection operation fails on a named collection, the server SHALL carry collection as an identifier argument.

#### Scenario: Named collection refusal
- **WHEN** a collection read, create, delete or empty check fails on a named collection
- **THEN** the answer carries collection in errorArgs.

### Requirement: Collection unit reference

When a collection change names a unit, the server SHALL carry unit as an identifier argument.

#### Scenario: Unit edge refusal
- **WHEN** a collection change or unit no-collection refusal names a unit
- **THEN** the answer carries unit in errorArgs.

### Requirement: Lock target

When a manual lock operation fails on a named target, the server SHALL carry target and targetKind as identifier arguments.

#### Scenario: Lock refusal
- **WHEN** a manual lock read, lock, unlock or change fails on a named target
- **THEN** the answer carries target and targetKind in errorArgs.

### Requirement: Lock holder and time

When a lock request meets a held lock, the server SHALL carry holder as an identifier argument and since as a time argument.

#### Scenario: Held lock
- **WHEN** a lock request meets a held lock
- **THEN** the answer carries holder and since in errorArgs.

### Requirement: Context field and limit

When a context write breaks a field rule, the server SHALL carry field as an identifier argument.

#### Scenario: Field rule breach
- **WHEN** a context write breaks a field rule
- **THEN** the answer carries field and, for a rule with a bound, limit in errorArgs.
