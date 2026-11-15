## ADDED Requirements

### Requirement: Record primary

The server SHALL store exactly one primary installation ID for each namespace, set when the namespace is created.

#### Scenario: Read placement
- **WHEN** A principal with a namespace read grant reads the settings of the namespace, which calls GetPrimary.
- **THEN** The answer names the stored primary installation ID.

### Requirement: Refuse non-primary write

When a namespace write reaches an installation that is not the primary installation, the server SHALL answer 421 with the error code federation.not-primary.

#### Scenario: Write on non-primary
- **WHEN** A write names a namespace whose primary ID differs from the caller installation, so RequirePrimary fails.
- **THEN** The server answers 421, names the primary installation ID and writes an audit entry.

#### Scenario: Missing primary
- **WHEN** RequirePrimary finds no primary ID or no caller installation ID.
- **THEN** The server answers 421 and changes nothing.

### Requirement: Reads stay local

While a request is a read, the server SHALL serve it from local data.

#### Scenario: Read on non-primary
- **WHEN** A read names a namespace on an installation that is not its primary.
- **THEN** The server answers from local data and never answers 421.
