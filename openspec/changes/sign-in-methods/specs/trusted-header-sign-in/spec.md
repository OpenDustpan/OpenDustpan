## ADDED Requirements

### Requirement: Proxy list gate

When trusted-header sign-in is on, the server SHALL accept the signed header token only from a TCP peer on the proxy list of that provider.

#### Scenario: Unnamed proxy
- **WHEN** the header arrives from a TCP peer outside the list
- **THEN** the server gives 401 with code `sign-in.untrusted-proxy` (NEW), opens no session, and writes an audit entry with the TCP peer address.

#### Scenario: Spoofed forwarded header
- **WHEN** a request from an unlisted TCP peer carries an X-Forwarded-For header with a listed address
- **THEN** the server gives 401 with code `sign-in.untrusted-proxy` and records the TCP peer address, not the forwarded address.

#### Scenario: IPv6 range
- **WHEN** the TCP peer is an IPv6 address inside a listed IPv6 CIDR range and the token is valid
- **THEN** the server accepts the token.

#### Scenario: Empty list
- **WHEN** the proxy list of the provider is empty
- **THEN** the provider is off and the server opens no session.

### Requirement: Signed token mode only

The server SHALL accept a header provider only in the `jwt` mode.

#### Scenario: Plain mode refused
- **WHEN** an administrator creates a header provider with the mode `plain`
- **THEN** the server gives 400 and stores no provider.
