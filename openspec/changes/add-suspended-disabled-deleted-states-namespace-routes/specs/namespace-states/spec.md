## ADDED Requirements

### Requirement: Suspended read-only

While a namespace is suspended, the server SHALL refuse new work and other writes with error code namespace.suspended.

#### Scenario: Suspended write refused
- **WHEN** Client sends a write or starts covered work in a suspended namespace
- **THEN** Server answers 409 with error code namespace.suspended and changes nothing.

#### Scenario: Suspended reads pass
- **WHEN** Client reads state, history, or backup export in a suspended namespace
- **THEN** Server answers reads, sign-in reads and exports as granted.

### Requirement: Suspended allowlist

While a namespace is suspended, the server SHALL answer reads, sign-in reads and backup exports that the caller may do.

#### Scenario: Exports and sign-in pass
- **WHEN** Client requests a backup export or a sign-in read in a suspended namespace
- **THEN** Server answers the export and the sign-in read.

### Requirement: SuspendNamespace

When an operator with installation:suspend-namespace calls SuspendNamespace with a reason, the server SHALL set state suspended.

#### Scenario: SuspendNamespace sets suspended
- **WHEN** Operator with installation:suspend-namespace calls SuspendNamespace with a reason
- **THEN** Server sets the state to suspended and writes an audit entry in installation and namespace scopes with reason and actor.

#### Scenario: SuspendNamespace refused
- **WHEN** Caller misses the grant or field or names a bad state
- **THEN** Server answers 403 without the action, 400 without a reason, 404 for an unknown namespace, and 409 for an already suspended namespace.

#### Scenario: SuspendNamespace idempotent replay
- **WHEN** Operator repeats SuspendNamespace with the same Idempotency-Key and body
- **THEN** Server answers the stored answer without a second state change.

### Requirement: ResumeNamespace

When an operator with installation:suspend-namespace calls ResumeNamespace with a reason on a suspended namespace, the server SHALL set state active.

#### Scenario: ResumeNamespace sets active
- **WHEN** Operator with installation:suspend-namespace calls ResumeNamespace with a reason
- **THEN** Server sets the state to active and writes an audit entry in installation and namespace scopes with reason and actor.

#### Scenario: ResumeNamespace of disabled refused
- **WHEN** Operator calls ResumeNamespace on a disabled or deleted namespace
- **THEN** Server answers 409 and changes nothing.

### Requirement: Running work safe

When an operator suspends a namespace, the server SHALL let running jobs run to end and refuse new jobs.

#### Scenario: Running jobs end
- **WHEN** Operator suspends a namespace with running jobs
- **THEN** Running jobs run to end and new job starts are refused with namespace.suspended.

### Requirement: Disabled and deleted closed

While a namespace is disabled, the server SHALL refuse writes with error code namespace.disabled.

#### Scenario: Disabled refused
- **WHEN** Client sends any write in a disabled namespace
- **THEN** Server answers 403 with error code namespace.disabled and changes nothing.

#### Scenario: Deleted hidden
- **WHEN** Client reads or writes a deleted namespace
- **THEN** Server answers 404 with no namespace data.
