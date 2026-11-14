## ADDED Requirements

### Requirement: Allowed networks setting

The server SHALL expose profileProviderNetworks and profileCACertificates as installation settings with an empty default.

#### Scenario: Empty default
- **WHEN** profileProviderNetworks is empty
- **THEN** the dial fails with ErrUnavailable for each private, link-local and loopback address.

#### Scenario: Scoped host port
- **WHEN** profileProviderNetworks lists a host:port with namespaces
- **THEN** the dial succeeds for a listed namespace and fails elsewhere.

### Requirement: CA certificates setting

When an operator saves profileCACertificates, the server SHALL trust only the valid PEM bundle that the operator saved.

#### Scenario: Bad PEM
- **WHEN** the value is not a valid PEM bundle
- **THEN** the save fails with ErrInvalid and the stored value stays unchanged.

### Requirement: Setting value guard

When an operator saves profileProviderNetworks, the server SHALL refuse a /0 network, a link-local range and a loopback address.

#### Scenario: Refused network entry
- **WHEN** an entry names /0, a link-local range or a loopback address
- **THEN** the save fails with ErrInvalid and the stored value stays unchanged.

### Requirement: Setting change record

When an operator changes a profile provider setting, the server SHALL write an audit entry for the change.

#### Scenario: Setting change audit
- **WHEN** an operator changes a profile provider setting
- **THEN** the server writes an audit entry with the principal and the new value.

### Requirement: Weaker setting needs fresh step

When an operator widens profileProviderNetworks, the server SHALL need a fresh authentication step for the change.

#### Scenario: Fresh step missing
- **WHEN** an operator widens the list without a fresh step
- **THEN** the server answers 403 with the error code mfa.fresh-step-required and changes nothing.
