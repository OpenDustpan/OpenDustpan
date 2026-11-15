## ADDED Requirements

### Requirement: Proxy list gate

When trusted-header sign-in is on, the server SHALL accept the signed header token only from an address on the proxy list of that provider.

#### Scenario: Unnamed proxy
- **WHEN** the header arrives from an address outside the list
- **THEN** the server gives 401 with code `sign-in.untrusted-proxy` (NEW) and writes the source address to the audit log.

#### Scenario: Spoofed forwarded header
- **WHEN** a request from an unlisted peer carries an X-Forwarded-For with a listed address
- **THEN** the server gives 401 and uses the peer address only.

#### Scenario: CIDR and IPv6
- **WHEN** the peer is an IPv6 address inside a listed CIDR
- **THEN** the server accepts the token.
