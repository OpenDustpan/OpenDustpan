## ADDED Requirements

### Requirement: Immutable subject only

If a provider save names a mutable subject source, then the server SHALL refuse the save with the error code `sign-in.subject-mutable`.

#### Scenario: Mutable source refused
- **WHEN** a site administrator saves CAS with no attribute, CAS with mail, LDAP with uid, OAuth with email or header in plain mode
- **THEN** the server answers 400 with `sign-in.subject-mutable`

#### Scenario: Partial PATCH checked
- **WHEN** a PATCH changes one field of a stored provider and the merged settings hold a mutable source
- **THEN** the server answers 400 with `sign-in.subject-mutable`

### Requirement: Allowed source accepted

When a provider save names an allowed subject source, the server SHALL store the provider.

#### Scenario: Allowed sources saved
- **WHEN** a site administrator saves CAS with a named ID attribute, LDAP with entryUUID or objectGUID, OAuth with sub or header in jwt mode with sub
- **THEN** the server stores the provider and shows the subject source

### Requirement: Stored mutable provider

While a stored provider has a mutable subject source, the server SHALL refuse each sign-in through that provider.

#### Scenario: Old config fails closed
- **WHEN** a person signs in through a provider saved with a mutable source, or restored from a backup with one
- **THEN** the server answers `sign-in.unavailable` and writes one audit entry for each provider and each hour

#### Scenario: Administrator turns it on
- **WHEN** the administrator saves an allowed source in the provider
- **THEN** the provider signs in again

### Requirement: Missing subject

If an assertion has no value for the subject source, then the server SHALL refuse the sign-in.

#### Scenario: No entryUUID
- **WHEN** the directory returns an entry with no entryUUID
- **THEN** the sign-in is refused and no principal is made

#### Scenario: Bad jwt sub
- **WHEN** a header jwt has an empty sub or a sub with characters outside the allowlist
- **THEN** the sign-in is refused and no principal is made
