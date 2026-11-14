## ADDED Requirements

### Requirement: Serve versions to signed-in caller

When a signed-in caller sends GET /api/dustpan/v1/capabilities, the server SHALL answer the NEW fields `serverVersion` and `descriptionVersion`.

#### Scenario: Collection-only token reads versions via capabilities
- **WHEN** caller with caller:read and only collection actions sends GET /api/dustpan/v1/capabilities
- **THEN** response holds apiVersion unchanged, serverVersion equal to binary version, and descriptionVersion equal to info.version

#### Scenario: Version forms pass through as held
- **WHEN** capabilities method serves release, dev, and describe builds
- **THEN** serverVersion equals binary string for release, dev, and describe forms

#### Scenario: Unset version sends dev
- **WHEN** binary version string is empty or unset
- **THEN** serverVersion equals dev

### Requirement: Refuse anonymous version read

If a request has no credential, then the server SHALL refuse it with 401.

#### Scenario: No credential gets 401
- **WHEN** request has no credential
- **THEN** server answers 401 and sends no version values

### Requirement: Refuse invalid credential

If a request carries an invalid, expired, or revoked credential, then the server SHALL refuse it with 401.

#### Scenario: Expired credential gets 401
- **WHEN** request carries an invalid, expired, or revoked credential
- **THEN** server answers 401 and sends no version values

### Requirement: Keep existing version surfaces unchanged

While the server serves versions on capabilities, the server SHALL keep apiVersion and ANY/GET /api/dustpan/v1/upgrade-check unchanged.

#### Scenario: apiVersion stays
- **WHEN** signed-in caller reads capabilities
- **THEN** apiVersion equals v1

#### Scenario: Upgrade-check keeps gate
- **WHEN** caller sends ANY/GET /api/dustpan/v1/upgrade-check
- **THEN** server still needs server:observe on the installation
