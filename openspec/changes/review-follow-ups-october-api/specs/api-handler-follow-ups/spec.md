## ADDED Requirements

### Requirement: served-release-indicator

When protocol eligibility rules refuse a release, the server SHALL list the release with served set to false.

#### Scenario: served-release-true
- **WHEN** protocol eligibility rules allow the release
- **THEN** the release remains listed with served true.

#### Scenario: key-removal-marks-unserved
- **WHEN** a signing key is removed so protocol rules refuse a release
- **THEN** the release remains listed with served false.

### Requirement: operator-field-presence

When a git-host connection request includes an operator-owned field, the server SHALL refuse the request with 400.

#### Scenario: null-operator-field-refused
- **WHEN** a request includes apiAddress, appID, privateKey, eventSecret, credential, or credentials with null or non-null value
- **THEN** the server refuses the request with 400.

#### Scenario: omitted-fields-succeed
- **WHEN** a request omits those fields
- **THEN** an otherwise-valid request succeeds.

### Requirement: forge-invalid-text

When a forge value is invalid, the server SHALL return 400 with the public reason and without internal wrapper prefixes.

#### Scenario: wrapped-invalid-keeps-public-reason
- **WHEN** the handler receives a wrapped forge invalid value with a validation reason
- **THEN** the 400 response includes the public reason once and excludes wrapper prefixes.

#### Scenario: empty-reason-fixed-answer
- **WHEN** the validation reason is empty or holds a wrapper-like string
- **THEN** the 400 response holds the fixed public text with no wrapper prefix.

### Requirement: connection-id-in-audit

When a connection delete or make-default operation is refused, the server SHALL include the requested connection ID in the audit entry.

#### Scenario: refused-delete-names-connection
- **WHEN** a delete or make-default is refused for a named connection
- **THEN** the audit entry includes the connection ID within the same namespace scope.

#### Scenario: failed-create-names-no-id
- **WHEN** a connection create fails before an ID exists
- **THEN** the audit entry holds no connection ID.

### Requirement: fresh-auth-sign-in-path

When fresh authentication is refused with 403, the server SHALL include a usable sign-in start path in the reauthenticate field.

#### Scenario: token-gets-sign-in-path
- **WHEN** an access token reaches a fresh-authentication-protected Pulumi route
- **THEN** the 403 response includes the console sign-in start path and keeps no-store behavior.

#### Scenario: stale-session-keeps-401
- **WHEN** a stale session reaches the same route
- **THEN** the response keeps 401 status and the provider-specific path.

### Requirement: nullable-map-rule

When a map field is nil without omitempty, the server SHALL mark the map nullable and write null in JSON output.

#### Scenario: nil-map-follows-rule
- **WHEN** a nil map field without omitempty is described and encoded
- **THEN** the schema marks the map nullable and the JSON output writes null.

### Requirement: embedded-required-refused

Where field discovery finds a required marker on a field promoted through an embedded pointer, the server SHALL refuse the shape with a descriptive discovery error.

#### Scenario: embedded-required-errors
- **WHEN** a required marker sits on a field promoted through an embedded pointer
- **THEN** field discovery refuses the shape with a descriptive error.
