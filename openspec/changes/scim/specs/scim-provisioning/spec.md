## ADDED Requirements

### Requirement: SCIM auth scope

When a SCIM request lacks a SCIM-scoped credential, the server SHALL answer 401 and write no row.

#### Scenario: Missing or wrong scope
- **WHEN** a SCIM create or DELETE arrives with no token, a wrong token, or a token without SCIM scope
- **THEN** the server answers 401, writes no row, and writes an audit entry with an empty actor and the fingerprint of the credential used.

### Requirement: SCIM invalid body

When a SCIM request carries an unknown field, an oversize body, or a bad subject, the server SHALL refuse it and write no row.

#### Scenario: Extra fields and bad subject
- **WHEN** a SCIM request carries an unknown field, an oversize body, or a bad subject
- **THEN** the server refuses with 400, writes no row, and names the check command in the answer.

### Requirement: Provision user and group

When a SCIM client sends a valid create, the server SHALL store the user, the group, and the membership with stable IDs.

#### Scenario: Create and link
- **WHEN** a SCIM client creates a user and a group and adds the user to the group
- **THEN** the server stores the person with a stable subject, stores the group, stores the membership, and the user runs pulumi up and tofu apply through the group grant.

### Requirement: Local subject collision

When a SCIM subject collides with a local user subject, the server SHALL refuse the create or link with 409.

#### Scenario: Subject taken by local user
- **WHEN** a SCIM create or link uses a subject that a local user holds
- **THEN** the server answers 409, keeps the local user unchanged, and writes an audit entry for the refused link.

### Requirement: Protected group

When a SCIM membership targets a protected group without trust and without full sync, the server SHALL refuse the add.

#### Scenario: Protected group refuses SCIM add
- **WHEN** a SCIM membership targets a protected group that does not trust the identity provider and full sync is off
- **THEN** the server refuses the membership with 403 and writes an audit entry.

### Requirement: Update user and group

When a SCIM client sends a valid update, the server SHALL apply the named field and membership changes only.

#### Scenario: Patch name and membership
- **WHEN** a SCIM client sends a valid PATCH or PUT for a user or group
- **THEN** the server stores the new name, adds and removes the named memberships only, and writes an audit entry.

### Requirement: Duplicate create refused

When a SCIM create repeats a stored subject, the server SHALL refuse it with 409.

#### Scenario: Duplicate subject
- **WHEN** a SCIM client creates a user whose subject already exists
- **THEN** the server answers 409, keeps the stored user unchanged, and writes no audit entry.

### Requirement: Concurrent create

When two SCIM creates race on one subject, the server SHALL store one user and refuse the loser with 409.

#### Scenario: Race on subject
- **WHEN** two SCIM creates with the same subject race
- **THEN** one create stores the user and the other gets 409 with no second row and no extra audit entry.

### Requirement: Atomic writes

While a SCIM create or PATCH runs, the server SHALL commit the user, the group, and the membership changes in one transaction.

#### Scenario: Membership write fails on create
- **WHEN** a SCIM create fails on the membership write
- **THEN** the server rolls back the user and group write and answers 500 with the retry command.

#### Scenario: Membership write fails on patch
- **WHEN** a SCIM PATCH fails on a membership write
- **THEN** the server rolls back the whole PATCH and keeps prior fields and memberships unchanged.

### Requirement: Disable on removal

When a SCIM client deletes a user, the server SHALL disable the sessions and the access tokens of the user at once and keep them.

#### Scenario: DELETE disables
- **WHEN** a SCIM client deletes the user
- **THEN** the server disables the sessions and the access tokens, keeps the rows, writes an audit entry, and the next CLI request fails with 401.

### Requirement: In-flight request

When a SCIM removal disables a token with a request in flight, the server SHALL refuse each new request with that token.

#### Scenario: Disable during CLI call
- **WHEN** a SCIM DELETE disables a token while a CLI request with that token is in flight
- **THEN** the already checked call completes and each new call fails with 401.

### Requirement: Break-glass guard

When a SCIM DELETE targets the last enabled break-glass principal, the server SHALL refuse it with 409 without disable or revocation.

#### Scenario: Last break-glass refused
- **WHEN** a SCIM DELETE targets the last enabled break-glass principal
- **THEN** the server answers 409, keeps the principal enabled with tokens unchanged, and writes an audit entry for the refused removal.

#### Scenario: Race with restart
- **WHEN** a SCIM DELETE of a break-glass principal with sessions in flight races a restart
- **THEN** the guard refusal and its audit entry commit together with no partial token change.

### Requirement: Idempotent delete

When a SCIM client repeats a DELETE, the server SHALL answer without a second disable.

#### Scenario: Unknown or already disabled
- **WHEN** a SCIM client repeats a DELETE
- **THEN** the server answers 404 for an unknown user and 204 with no second disable entry for an already disabled user, and a retry after 429 counts as a new rate limit attempt.

### Requirement: Opt-in revocation

When the revocation setting is on, the server SHALL revoke the sessions and the access tokens of the removed user instead of disable.

#### Scenario: Setting revokes
- **WHEN** the revocation setting is on and a SCIM client deletes the user
- **THEN** the server revokes the sessions and the access tokens at once and writes an audit entry for each revocation.

### Requirement: Revocation setting audit and re-enable

When an administrator changes the revocation setting or re-enables a revoked user, the server SHALL write an audit entry for the change.

#### Scenario: Setting change and re-enable
- **WHEN** an administrator changes the revocation setting or re-enables a revoked user
- **THEN** the audit export lists the setting change and a re-enable restores sign-in only after a new grant and a new token.

### Requirement: Audit atomic

When the server disables or revokes on SCIM removal, the server SHALL commit the state change and its audit entry in one transaction.

#### Scenario: Restart between writes
- **WHEN** the server restarts between a SCIM removal and its audit write
- **THEN** the audit export shows the disable or revocation entry exactly when the disable or revocation shows.

#### Scenario: Break-glass in flight
- **WHEN** a SCIM DELETE of a break-glass principal with sessions in flight races a restart
- **THEN** the guard refusal and its audit entry commit together with no partial token change.

### Requirement: Rate limit removals

When a SCIM credential sends more than 100 DELETEs in 5 minutes, the server SHALL refuse the excess with 429.

#### Scenario: Burst refused
- **WHEN** a SCIM credential sends more than 100 DELETEs in 5 minutes
- **THEN** the server answers 429, keeps the extra users enabled, and writes an audit entry for the refused burst.

### Requirement: Audit

The server SHALL write an audit entry for each SCIM create, change, membership change, disable, and revocation.

#### Scenario: Entries exist
- **WHEN** SCIM writes and removals happen
- **THEN** the audit export lists the create, update, membership, disable, and revocation entries with actor and time.
