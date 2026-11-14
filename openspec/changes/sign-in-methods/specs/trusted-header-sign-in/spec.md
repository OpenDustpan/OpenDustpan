## ADDED Requirements

### Requirement: Proxy list gate

When trusted-header sign-in is on, the server SHALL accept the signed header token only from an address on the proxy list of that provider.

#### Scenario: Unnamed proxy
- **WHEN** the header arrives from an address outside the list
- **THEN** the server gives 401 with code `signin.untrusted-proxy` and writes the source address to the audit log.

### Requirement: Valid provider setting

If a header provider has an empty proxy list or no signed-token mode, then the server SHALL refuse to turn the provider on.

#### Scenario: Empty list
- **WHEN** an administrator turns on a header provider with an empty proxy list
- **THEN** the server refuses the change and the provider stays off.

### Requirement: Proxy list change record

When an administrator changes the proxy list of a header provider, the server SHALL write an audit entry for the change.

#### Scenario: List changed
- **WHEN** an administrator adds an address to the list
- **THEN** the audit entry holds the old list, the new list and the administrator.

### Requirement: Default off

While trusted-header sign-in is off, the server SHALL ignore the header.

#### Scenario: Default refuses
- **WHEN** an administrator has not turned the method on
- **THEN** the header gives no sign-in.

### Requirement: No plain header

If a header carries a plain user name with no signed token, then the server SHALL refuse the sign-in.

#### Scenario: Plain name
- **WHEN** a named proxy sends a plain user name header
- **THEN** the server gives 401 and no session.

### Requirement: No group claim

If a header token carries a group claim, then the server SHALL apply no group from it.

#### Scenario: Group ignored
- **WHEN** the signed token holds a groups claim
- **THEN** the person signs in and holds no membership from it.
