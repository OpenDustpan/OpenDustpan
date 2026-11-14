## ADDED Requirements

### Requirement: scim-gate

While the owner has not cleared the SCIM timing, the server SHALL answer 404 on the SCIM prefix.

#### Scenario: Not cleared
- **WHEN** the owner has not cleared SCIM timing
- **THEN** the SCIM prefix answers 404.

### Requirement: scim-auth

If a SCIM request has no SCIM-scoped credential, then the server SHALL answer 401 and write no row.

#### Scenario: Wrong token
- **WHEN** a create arrives with a wrong token
- **THEN** the server answers 401, writes no row and writes an audit entry.

### Requirement: provision

When a SCIM client sends a valid create, the server SHALL store the user, the group and the membership with stable IDs.

#### Scenario: Create and use
- **WHEN** a client creates a user and a group and adds the user
- **THEN** the user runs the Pulumi CLI and OpenTofu through the group grant.

### Requirement: scim-source

When SCIM creates a person, the server SHALL store the person with the source scim.

#### Scenario: Source
- **WHEN** SCIM creates a person
- **THEN** the person has the source scim and the same subject on each update.

### Requirement: protected-group

If a SCIM membership targets a protected group that does not trust the identity provider, then the server SHALL refuse the add with 403.

#### Scenario: Protected
- **WHEN** a membership targets such a group
- **THEN** the server answers 403 and writes an audit entry.

### Requirement: duplicate

If a SCIM create repeats a stored subject, then the server SHALL answer 409.

#### Scenario: Duplicate
- **WHEN** a create repeats a subject
- **THEN** the server answers 409 and keeps the stored user.

### Requirement: disable-on-removal

When a SCIM client deletes a user, the server SHALL disable the sessions and the access tokens of the user and keep them.

#### Scenario: Delete
- **WHEN** a client deletes a user
- **THEN** the person is disabled, the session generation grows by 1, the rows stay and the next CLI command gives 401.

### Requirement: opt-in-revocation

Where the revocation setting is on, the server SHALL revoke the access tokens of a removed user.

#### Scenario: Revoke
- **WHEN** the setting is on and a client deletes a user
- **THEN** the tokens are revoked and each revocation has an audit entry.

### Requirement: break-glass-guard

If a SCIM removal targets the last enabled break-glass principal, then the server SHALL answer 409 with no disable.

#### Scenario: Last one
- **WHEN** a DELETE targets that principal
- **THEN** the server answers 409 and changes no session or token.

### Requirement: rate-limit

If a SCIM credential sends DELETEs above the removal limit, then the server SHALL answer 429 for the excess.

#### Scenario: Burst
- **WHEN** a credential sends 101 DELETEs in 5 minutes
- **THEN** the server answers 429 for the excess and keeps those users enabled.

### Requirement: audit

The server SHALL write an audit entry for each SCIM write, disable and revocation in the same transaction as the change.

#### Scenario: Entries
- **WHEN** SCIM writes and removals happen
- **THEN** the audit export lists each entry with actor and time.
