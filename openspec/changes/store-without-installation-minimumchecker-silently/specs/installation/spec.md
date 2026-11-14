## ADDED Requirements

### Requirement: Refuse store without minimum check

When the store given to NewServer lacks installation.MinimumChecker, the server SHALL refuse construction with an error that wraps ErrMissingOption.

#### Scenario: Store without MinimumChecker at startup
- **WHEN** the store given to NewServer lacks installation.MinimumChecker
- **THEN** construction fails with an error that wraps ErrMissingOption and the server does not start.

#### Scenario: Wrapper hides MinimumChecker
- **WHEN** a decorating store wrapper does not expose installation.MinimumChecker
- **THEN** construction fails with an error that wraps ErrMissingOption.

### Requirement: Refuse nil maintenance service

When the maintenance service given to NewServer is nil, the server SHALL refuse construction with an error that wraps ErrMissingOption.

#### Scenario: Nil maintenance service
- **WHEN** the maintenance service given to NewServer is nil
- **THEN** construction fails with an error that wraps ErrMissingOption.

### Requirement: Refuse store without store-check rows

When the store lacks the restore rows that the store check needs, the server SHALL refuse construction with an error that wraps ErrMissingOption.

#### Scenario: Store without restore rows
- **WHEN** the store lacks the restore rows that the store check needs
- **THEN** construction fails with an error that wraps ErrMissingOption.

### Requirement: Refuse purge gauge without store support

When the store lacks the purge records that the backlog gauge needs, the server SHALL refuse construction with an error that wraps ErrMissingOption.

#### Scenario: Purge backlog gauge without store support
- **WHEN** the store lacks the purge records that the backlog gauge needs
- **THEN** construction fails with an error that wraps ErrMissingOption instead of running without the gauge.

### Requirement: Keep per-call minimum check

When restore start, worker check or readiness meets a store without a usable minimum version, the server SHALL return the minimum-check error instead of skipping the check.

#### Scenario: Swapped store after construction
- **WHEN** restore start, worker check or readiness meets a store without a usable minimum version
- **THEN** the call returns the minimum-check error instead of skipping the check.

### Requirement: MinimumChecker conformance accept

When the binary meets the minimum that the database requires, the store SHALL return nil from CheckBinaryMinimum.

#### Scenario: Binary at minimum
- **WHEN** the binary meets the minimum that the database requires
- **THEN** CheckBinaryMinimum returns nil.

### Requirement: MinimumChecker conformance refuse

When the binary is below the minimum that the database requires, the store SHALL return an error from CheckBinaryMinimum.

#### Scenario: Binary below minimum
- **WHEN** the binary is below the minimum that the database requires
- **THEN** CheckBinaryMinimum returns an error that names the minimum.

### Requirement: MinimumChecker conformance read failure

When the version read fails, the store SHALL return an error from CheckBinaryMinimum.

#### Scenario: Version read failure
- **WHEN** the version read fails
- **THEN** CheckBinaryMinimum returns an error.
