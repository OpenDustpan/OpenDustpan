## ADDED Requirements

### Requirement: refuse-plain-by-default

When the SMTP relay offers no TLS, the server SHALL refuse the connection unless the plain-connections setting is on.

#### Scenario: Plain relay with opt-in off
- **WHEN** the relay offers no TLS and the plain-connections setting is off
- **THEN** the server refuses the dial, sends no credentials, and returns the plain-refused code

### Requirement: loopback-plain-without-opt-in

When the connected peer IP address is loopback, the server SHALL allow a plain SMTP connection without the opt-in.

#### Scenario: Loopback relay with opt-in off
- **WHEN** the connected peer IP is loopback and the setting is off
- **THEN** the server allows a plain connection to the loopback address

#### Scenario: Hostname that resolves outside loopback
- **WHEN** the hostname resolves to a non-loopback peer IP and the setting is off
- **THEN** the server refuses the plain connection

### Requirement: read-opt-in-each-dial

When the server opens each SMTP connection, the server SHALL read the current effective value of the plain-connections setting at the point of use.

#### Scenario: Setting flips between dials
- **WHEN** the plain-connections setting changes between two connections
- **THEN** the second dial follows the new value

### Requirement: fail-closed-on-bad-setting

If the plain-connections value is missing, unreadable, or invalid at dial time, then the server SHALL treat the value as off.

#### Scenario: Missing or invalid value
- **WHEN** the plain-connections setting is missing, unreadable, or invalid at dial time
- **THEN** the server treats the value as off and refuses the plain dial

### Requirement: require-tls-upgrade

When the SMTP port negotiates STARTTLS, the server SHALL complete the TLS upgrade before any AUTH step.

#### Scenario: Stripped STARTTLS advertisement
- **WHEN** the relay hides STARTTLS on a port that must upgrade
- **THEN** the server refuses the connection with a downgrade error

### Requirement: refuse-plain-credentials

When an SMTP connection has no TLS, the server SHALL refuse to send AUTH credentials except to a loopback peer IP.

#### Scenario: Plain AUTH with opt-in on
- **WHEN** the connection has no TLS even though the plain-connections setting is on
- **THEN** the server refuses to send the credential

### Requirement: keep-tls-checks

When the SMTP connection uses TLS, the server SHALL apply full certificate checks with no skip.

#### Scenario: Expired certificate with setting off
- **WHEN** the relay presents an expired certificate
- **THEN** the server refuses the connection with a certificate error

#### Scenario: Hostname mismatch with setting off
- **WHEN** the relay certificate name does not match the relay host
- **THEN** the server refuses the connection with a certificate error

#### Scenario: Untrusted root with setting on
- **WHEN** the relay presents an untrusted chain and the plain-connections setting is on
- **THEN** the server refuses the connection with a certificate error

### Requirement: refusal-content

When the server refuses a plain SMTP dial, the server SHALL return the NEW error code for plain refusal with the relay address and the settings call.

#### Scenario: Plain refusal text
- **WHEN** the server refuses a plain SMTP dial
- **THEN** the refusal names the relay address, the plain-connections setting, and PATCH /api/dustpan/v1/settings
