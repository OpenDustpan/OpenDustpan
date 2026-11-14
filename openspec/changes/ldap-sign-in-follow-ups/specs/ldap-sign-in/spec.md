## ADDED Requirements

### Requirement: Group deadline covers rebind

When an LDAP group phase starts, the server SHALL start the 30 s group deadline before the service-account rebind.

#### Scenario: Slow rebind
- **WHEN** the service-account rebind stalls for 11 s
- **THEN** the server closes the directory connection, refuses the sign-in with 503 `sign-in.unavailable` and writes an audit entry with no directory text

### Requirement: Group search limit

If a group search returns more than 1000 entries, then the server SHALL refuse the sign-in.

#### Scenario: 1001 entries
- **WHEN** the directory returns 1001 entries
- **THEN** the sign-in is refused with 503 `sign-in.unavailable` and no partial groups

### Requirement: Fixed refusal reasons

If checkEnabled or the bind gate refuses a sign-in, then the server SHALL answer a fixed reason without directory text.

#### Scenario: Disabled user
- **WHEN** the bind gate refuses a subject with an error that holds directory text
- **THEN** the answer is 401 `sign-in.failed` with the text "The user name or the password is wrong." and the audit entry holds the error class

### Requirement: Binary good sign-in

When a user signs in with a good password through the built binary, the server SHALL answer 200 with a session.

#### Scenario: Good password
- **WHEN** the binary starts with the password file and the allowed range of the private directory
- **THEN** the sign-in answers 200 and the password variable is absent from the process environment

### Requirement: Binary wrong password

When a user signs in with a wrong password through the built binary, the server SHALL answer 401 `sign-in.failed`.

#### Scenario: Wrong password
- **WHEN** the sign-in uses a wrong password
- **THEN** the answer is 401 with the text "The user name or the password is wrong."

### Requirement: Plain LDAP refused

If the directory is a plain LDAP address that is not loopback and no allowed range holds it, then the server SHALL refuse the connection.

#### Scenario: No allowed range
- **WHEN** the binary starts without --ldap-allowed-ranges for the private address
- **THEN** the refusal names the address, the setting --ldap-allowed-ranges and the command `opendustpan host show-settings`
