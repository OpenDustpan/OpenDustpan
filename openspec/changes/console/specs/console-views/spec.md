## ADDED Requirements

### Requirement: Member add browser test

While the console runs against the suite server, the server SHALL show a member added through the console in the member list.

#### Scenario: Add member in browser
- **WHEN** a site administrator adds a member through the console against the suite server
- **THEN** the member list shows the new member and the server writes one audit entry.

### Requirement: Group make browser test

While the console runs against the suite server, the server SHALL show a group made through the console in the group list.

#### Scenario: Make group in browser
- **WHEN** a site administrator makes a group through the console against the suite server
- **THEN** the group list shows the new group and the server writes one audit entry.

### Requirement: Grant give and revoke browser test

While the console runs against the suite server, the server SHALL show a grant given and revoked through the console in the grant list.

#### Scenario: Give and revoke grant in browser
- **WHEN** a site administrator gives a grant on a collection and revokes it through the console
- **THEN** the grant list shows the grant and then shows it ended.

### Requirement: Export download browser test

When a caller downloads the snapshot export through the console, the server SHALL answer each row with principal or group, role, object, grant time and last use.

#### Scenario: Download snapshot export
- **WHEN** a caller with the export action downloads the snapshot export through the console
- **THEN** each row names the principal or the group, the role, the object, the grant time and the last use, with Never when never used.

### Requirement: Expiring token banner

When a person holds a token within the warning period, the server SHALL mark the token as expiring soon in the caller answer.

#### Scenario: Show banner before expiry
- **WHEN** a person signs in with a token that expires within the warning period
- **THEN** the console shows the expiring-token banner with a renew link to token create.

### Requirement: Locked section reason

When a principal lacks the grant for a console section object, the server SHALL answer through the access endpoint with a generic reason.

#### Scenario: Open locked section
- **WHEN** a principal opens a console section without the grant for its object
- **THEN** the console shows a generic reason that names the grant to ask for and hides whether the object exists.

#### Scenario: Same answer for unseen and nonexistent
- **WHEN** a principal asks for the access of an unseen object and a nonexistent object
- **THEN** the server answers the same status, body and reason for both objects.

### Requirement: Denied grant surface

When a section open is refused for lack of grant, the server SHALL answer 403 grant.forbidden with the generic reason.

#### Scenario: Grant refused
- **WHEN** a principal without the grant opens a section
- **THEN** the console shows 403 grant.forbidden with the generic reason and the grant to ask for.

### Requirement: Expired token surface

When a request carries an expired access token, the server SHALL answer 401 token.expired.

#### Scenario: Expired token
- **WHEN** a request carries an expired access token
- **THEN** the console shows 401 token.expired and directs the person to sign in again before token create.
