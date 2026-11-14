## ADDED Requirements

### Requirement: Manage stored providers

The server SHALL provide create, read, update and delete routes for stored LDAP providers.

#### Scenario: Create
- **WHEN** a site administrator creates a stored LDAP provider
- **THEN** the server stores it and lists the effective values

#### Scenario: Reserved ID
- **WHEN** the ID equals the static provider ID
- **THEN** the server refuses the create with 409 and names the reserved ID

#### Scenario: Duplicate URL
- **WHEN** two requests race on the same directory URL
- **THEN** one answers 409 and the database holds one record

### Requirement: Fix the directory URL

If an update changes the directory URL of a stored provider, then the server SHALL refuse the update with 409.

#### Scenario: URL change
- **WHEN** an update names another URL
- **THEN** the server answers 409, names the rebind route and keeps the record

### Requirement: Delete a provider

When a site administrator deletes a stored provider, the server SHALL revoke the sessions of the provider and keep its ID reserved.

#### Scenario: Delete
- **WHEN** a provider is deleted
- **THEN** its sessions end and a create with the same ID answers 409

### Requirement: Reference the bind password

The server SHALL accept the bind password reference of a provider only when it names an environment variable or a file from the allowlist of the operator.

#### Scenario: Never returned
- **WHEN** a site administrator reads a provider
- **THEN** the answer shows the reference and no password

#### Scenario: Outside allowlist
- **WHEN** a create names a variable that is not on the allowlist
- **THEN** the server answers 400 and stores nothing

#### Scenario: Unreadable value
- **WHEN** the variable is empty or the file is missing at sign-in
- **THEN** the sign-in is refused

#### Scenario: Symlink
- **WHEN** the file path leaves the allowed root through a symlink
- **THEN** the server refuses to read it

### Requirement: Refuse plain connections

If a provider uses a plain connection to a non-loopback address while the plain-connection setting is off, then the server SHALL refuse the connection.

#### Scenario: Plain refused
- **WHEN** a create names an ldap:// URL to a non-loopback host
- **THEN** the server answers 400 with the address and the setting name

#### Scenario: Setting not valid
- **WHEN** the plain-connection setting is missing or not valid
- **THEN** the server treats it as off and refuses the plain URL

#### Scenario: Loopback allowed
- **WHEN** a create names an ldap:// URL to a loopback address
- **THEN** the server accepts the provider

#### Scenario: Setting turned off
- **WHEN** the setting changes to off after a plain provider exists
- **THEN** the next sign-in through that provider is refused and the record stays

#### Scenario: Static provider
- **WHEN** the static provider uses a plain URL while the setting is off
- **THEN** the sign-in is refused

### Requirement: Keep certificate checks

If the certificate of the directory fails the check, then the server SHALL refuse the sign-in.

#### Scenario: Bad certificate
- **WHEN** the directory certificate is not signed by the CA bundle
- **THEN** the sign-in is refused

#### Scenario: StartTLS
- **WHEN** a provider uses StartTLS and the upgrade fails
- **THEN** the sign-in is refused

#### Scenario: Unreachable
- **WHEN** the directory does not answer in 10 seconds
- **THEN** the sign-in is refused

### Requirement: Restrict subject source

The server SHALL accept only entryUUID or objectGUID as the subject attribute of an LDAP provider.

#### Scenario: Mail refused
- **WHEN** a create names the mail attribute as subject
- **THEN** the server answers 400

### Requirement: Bound user name

If the user name is empty, longer than 256 bytes or holds a control character, then the server SHALL refuse the sign-in before any bind.

#### Scenario: Control character
- **WHEN** the user name holds a control character
- **THEN** the sign-in is refused before any connection

### Requirement: Refuse unsafe sign-in

If the directory reports the user disabled, the bind fails or the user is absent, then the server SHALL refuse the sign-in.

#### Scenario: Disabled user
- **WHEN** the directory reports the user disabled
- **THEN** the sign-in is refused and an audit entry is written

#### Scenario: Empty password
- **WHEN** the password is empty
- **THEN** the sign-in is refused before any bind

#### Scenario: Referral
- **WHEN** the directory answers with a referral only
- **THEN** the sign-in is refused and no referral is followed

#### Scenario: Absent user
- **WHEN** the directory has no entry for the user
- **THEN** the sign-in is refused and no membership changes

### Requirement: Limit directory answers

If a group search returns more than 1000 entries, then the server SHALL refuse the sign-in.

#### Scenario: Oversize answer
- **WHEN** the group search returns 1001 entries
- **THEN** the sign-in is refused with no partial result

### Requirement: Apply LDAP groups

When an LDAP sign-in returns groups, the server SHALL apply them through the one membership function with source ldap under the protected-group rules.

#### Scenario: Applied
- **WHEN** an LDAP sign-in returns group memberships, nested groups included
- **THEN** memberships change as a flat list with source ldap and each change has an audit entry

#### Scenario: Protected
- **WHEN** a claim names a protected group
- **THEN** the server skips the add and writes an audit entry

### Requirement: Count removals

When an LDAP sign-in removes group memberships, the server SHALL count the removals with the source kind of the provider for the mass-removal alert.

#### Scenario: Removal counted
- **WHEN** a sign-in removes three memberships
- **THEN** the count rises by three with source kind ldap

### Requirement: Bind static directory

If the static LDAP provider ID points at another directory URL than the recorded one, then the server SHALL refuse to start.

#### Scenario: Changed URL
- **WHEN** the URL differs from the record
- **THEN** the server refuses to start and names the ID, old URL, new URL and the rebind route

#### Scenario: Same URL
- **WHEN** the URL equals the record
- **THEN** the server starts

#### Scenario: First start
- **WHEN** no row exists and the start marker is not set
- **THEN** the server writes the row and the marker in one transaction and starts

#### Scenario: Concurrent first start
- **WHEN** two servers start together with no row
- **THEN** one row exists and the other server reads it

#### Scenario: Lost record
- **WHEN** the row is missing and the start marker is set
- **THEN** the server refuses to start and names the rebind route

### Requirement: Authorize rebind

If a caller lacks the sign-in management action, then the server SHALL refuse the rebind route.

#### Scenario: No action
- **WHEN** a caller without the action calls the rebind route
- **THEN** the server answers 403 and changes no record

### Requirement: Rebind on purpose

When a site administrator with the sign-in management action calls the rebind route with a reason, the server SHALL change the recorded URL, revoke the sessions of the provider and write an audit entry.

#### Scenario: Rebind
- **WHEN** the rebind route is called with a reason and a valid URL
- **THEN** the next start with the new URL succeeds

#### Scenario: Audit content
- **WHEN** the rebind succeeds
- **THEN** the audit entry holds the caller, the reason, the old URL and the new URL
