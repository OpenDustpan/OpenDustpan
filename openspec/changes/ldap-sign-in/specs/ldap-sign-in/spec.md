## ADDED Requirements

### Requirement: An LDAP provider can be added and read

The server SHALL accept a sign-in provider in the mode `ldap` with the settings of
`docs/guides/ldap-sign-in.md`, validated with an allowlist, and SHALL show each
default and its effective value in the API.

#### Scenario: Valid provider
- **WHEN** a site administrator adds an `ldap` provider with valid settings
- **THEN** the server stores it and the answer shows each setting with its effective value

#### Scenario: Invalid setting
- **WHEN** the user filter does not contain `{user}`
- **THEN** the server refuses the provider and names the setting

### Requirement: A person signs in with a user name and a password

The server SHALL give a console session to a person whose user name and password
the directory accepts, through the stored provider of the person's home.

#### Scenario: Right password over TLS
- **WHEN** a person sends a right user name and password for an enabled provider that uses TLS
- **THEN** the server issues the session cookies

#### Scenario: Provider that is off or unknown
- **WHEN** a person sends credentials for a provider that is off or does not exist
- **THEN** the server answers as for an unknown provider and writes an audit entry

### Requirement: One answer for each failure

The server SHALL answer `401` with the text "The user name or the password is
wrong." for an unknown user name, a wrong password, a locked name, a disabled
person and an empty password, and SHALL write the real reason to the audit log.

#### Scenario: Wrong password
- **WHEN** the directory refuses the password
- **THEN** the answer is `401` with that text and the audit entry holds the reason

### Requirement: Plain connections are refused unless an operator opts in

The server MUST refuse an LDAP connection with no TLS to a host that is not a
loopback IP address, unless `egress.allowPlainConnections` is on. The server MUST
read the setting on each connection.

#### Scenario: Opt-in off
- **WHEN** the provider URL is `ldap://` to a non-loopback host and the setting is off
- **THEN** the sign-in is refused and the error names the address and the setting

#### Scenario: Opt-in on
- **WHEN** the setting is on
- **THEN** the sign-in over the plain connection succeeds for a right password

### Requirement: The bind password never appears

The server MUST NOT show the bind password or a typed password in a log, an
error, an audit entry or an API answer.

#### Scenario: Failed sign-in
- **WHEN** a sign-in fails
- **THEN** no log line, error text or audit entry holds a password
