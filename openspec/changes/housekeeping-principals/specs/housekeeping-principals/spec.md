## ADDED Requirements

### Requirement: One principal for each housekeeping purpose

The server SHALL make one built-in service principal for each housekeeping purpose at the install, and SHALL write its grants as stored grants with the install as the creator and the install time.

#### Scenario: Listed after the install
- **WHEN** an operator lists the principals of a new installation
- **THEN** each housekeeping purpose has one principal of kind `service` and source `builtin`, and each has stored grants with a creator and a time

### Requirement: Each sweep is attributed

Each housekeeping run SHALL authorize its action through the stored grants of its principal, and each audit entry of the run SHALL name that principal.

#### Scenario: Artifact sweep
- **WHEN** the artifact sweep deletes an expired artifact
- **THEN** the audit entry names the housekeeping principal of the artifact sweep, never `@server`

### Requirement: A revoke stops only its purpose

When an operator revokes the grant of one housekeeping principal, the server SHALL refuse only the work of that purpose, write a denial audit entry, and run the other purposes as before.

#### Scenario: Revoke the artifact sweep
- **WHEN** the grant of the artifact sweep principal is revoked
- **THEN** the next artifact sweep changes nothing and writes a denial, and the sweep of access tokens runs as before

### Requirement: No grant in code

The server SHALL decide each action of a housekeeping principal, and of any principal, only from stored grants. No rule in code SHALL give an action to a named principal.

#### Scenario: Old subject
- **WHEN** a principal with the subject `system:namespace-service:acme` asks for `unit:run-plan`
- **THEN** the server denies the action

### Requirement: No sign-in as a housekeeping principal

The server SHALL refuse each credential that names a built-in housekeeping principal, and SHALL refuse a grant to it of a role that its purpose does not hold.

#### Scenario: Token for a housekeeping principal
- **WHEN** an administrator asks for an access token of a housekeeping principal
- **THEN** the server answers 403 `principal.housekeeping` and makes no token

#### Scenario: Grant out of purpose
- **WHEN** an administrator grants `namespace-admin` to the artifact sweep principal
- **THEN** the server answers 422 `access.grant-out-of-purpose` and stores no grant
