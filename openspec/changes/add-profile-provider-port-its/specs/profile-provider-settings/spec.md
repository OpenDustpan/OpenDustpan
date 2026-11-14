## ADDED Requirements

### Requirement: Allowed networks setting

The server SHALL expose profileProviderNetworks and profileCACertificates as installation settings with an empty default that an operator changes.

#### Scenario: Scoped host port
- **WHEN** profileProviderNetworks lists a host:port with namespaces
- **THEN** the dial succeeds for a unit of a listed namespace and fails elsewhere.

### Requirement: Setting value guard

When an operator saves profileProviderNetworks, the server SHALL refuse a /0 network, a link-local range and a loopback address.

#### Scenario: Refused network entry
- **WHEN** profileProviderNetworks names /0, a link-local range or a loopback address
- **THEN** the save fails with ErrInvalid and the stored value stays unchanged.

### Requirement: Setting change record

When an operator changes a profile provider setting, the server SHALL write an audit entry for the change.

#### Scenario: Setting change audit
- **WHEN** an operator changes profileProviderNetworks or profileCACertificates
- **THEN** the server writes an audit entry with the principal and the new value and keeps the old value for each open in flight.
