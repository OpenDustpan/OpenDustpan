## ADDED Requirements

### Requirement: The remaining sweeps are attributed

The sweep of access tokens, the expiry of temporary administrators and the rotation of issuer keys SHALL authorize their action through the stored grants of their principals, and each audit entry of each run SHALL name that principal.

#### Scenario: Token sweep
- **WHEN** the sweep of access tokens ends an expired token
- **THEN** the audit entry names the housekeeping principal of the token sweep, never `@server`

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
