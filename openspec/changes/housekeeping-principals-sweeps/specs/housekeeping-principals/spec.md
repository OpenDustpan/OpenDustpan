## ADDED Requirements

### Requirement: Each sweep is attributed

Each housekeeping run SHALL authorize its action through the stored grants of its principal, and each audit entry of the run SHALL name that principal.

#### Scenario: Artifact sweep
- **WHEN** the artifact sweep deletes an expired artifact
- **THEN** the audit entry names the housekeeping principal of the artifact sweep, never `@server`

### Requirement: A revoke stops only its purpose

When an operator revokes the grant of one housekeeping principal, the server SHALL refuse only the work of that purpose, write a denial audit entry, and run the other purposes as before.

#### Scenario: Revoke the artifact sweep
- **WHEN** the grant of the artifact sweep principal is revoked
- **THEN** the next artifact sweep changes nothing and writes a denial, and the retention of history runs as before
