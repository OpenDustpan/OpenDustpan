## ADDED Requirements

### Requirement: Private host allowlist match

When a sink dials a private address, the server SHALL pass the dial only when the host is on the operator allowlist and the resolved IP is in its CIDRs.

#### Scenario: Private host allowlist match allowlisted host passes
- **WHEN** the host is on the list and the resolved IP is in its CIDRs
- **THEN** the dial passes and the audit entry holds the host and never the URL

#### Scenario: Sink create with allowlisted host passes
- **WHEN** a member creates a chat or webhook sink on an allowlisted private host
- **THEN** create succeeds and the audit entry records the member as actor and the host, never the URL

#### Scenario: Non-listed private host refused
- **WHEN** a sink posts to a private address with no matching entry
- **THEN** the client returns egress.ErrRefused and the audit entry holds the host only

#### Scenario: Partial multi-answer refused
- **WHEN** an allowlisted host returns several IPs and the dialed IP is outside its CIDRs
- **THEN** the dial is refused with egress.ErrRefused

### Requirement: Always refused ranges

While an allowlist entry exists, the server SHALL refuse loopback, link-local, fd00:ec2::254 and unspecified addresses.

#### Scenario: Loopback and link-local stay refused
- **WHEN** the address is loopback, 169.254.0.0/16, fd00:ec2::254 or unspecified, even with a list entry
- **THEN** the dial is refused with egress.ErrRefused

#### Scenario: IPv6 and mapped ranges refused
- **WHEN** the address is fc00::/7 or ::ffff:10.0.0.5 with no matching entry
- **THEN** the dial is refused with egress.ErrRefused

### Requirement: Redirect recheck

When a sink follows a redirect, the server SHALL check the new host and its resolved address against the same rules.

#### Scenario: Redirect to other host rechecked
- **WHEN** a sink follows a redirect to another host
- **THEN** credentials do not leave the first host and a refused second dial returns egress.ErrRefused

#### Scenario: Redirect class stays refused
- **WHEN** a chat webhook answers with 3xx under NoRedirect
- **THEN** the sender returns egress.ErrRefused and does not post twice

### Requirement: Invalid config refused at startup

If the operator allowlist has an invalid entry, then the server SHALL refuse startup and log the host without the URL.

#### Scenario: Bad CIDR refuses startup
- **WHEN** an entry has an invalid CIDR, a missing CIDR list or 0.0.0.0/0
- **THEN** startup fails and the log names the host without the URL

#### Scenario: Startup log shows hosts
- **WHEN** the server starts with a valid list
- **THEN** the log lists the allowed hosts without URLs

### Requirement: Exact host form

When the server reads an allowlist entry, the server SHALL accept only an exact lower-cased host with a required CIDR list.

#### Scenario: Wildcard refused
- **WHEN** an entry has a wildcard or upper case
- **THEN** startup is refused

#### Scenario: Unicode host refused at form check
- **WHEN** a sink host has hidden Unicode
- **THEN** create is refused with sink.invalid before any dial

### Requirement: Single-IP dial

When the server dials an allowlisted host, the server SHALL connect only to the checked IP with no second resolve.

#### Scenario: Multi-answer name uses checked IP
- **WHEN** a name returns one public and one private A record
- **THEN** the connect uses the checked IP and refuses the private answer

#### Scenario: Rebinding refused
- **WHEN** DNS changes the answer between connect and redirect
- **THEN** the second connect is checked again and refused with egress.ErrRefused

#### Scenario: Proxy ignored and SNI uses host
- **WHEN** a sink dials an allowlisted host with a proxy set in the environment
- **THEN** the dial ignores the proxy and TLS verifies the host name on the pinned IP

### Requirement: Legacy host read-path

When a stored chat sink holds a legacy host, the server SHALL use read-path validation at send time.

#### Scenario: Stored legacy sink still sends
- **WHEN** a stored chat sink holds a legacy host
- **THEN** send uses read-path validation and toDocument pins hostNeedsUpdate with an empty webhook target

#### Scenario: Legacy test class refused
- **WHEN** a legacy chat host fails the egress rules
- **THEN** the test route answers with the class refused
