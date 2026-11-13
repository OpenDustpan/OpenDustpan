## ADDED Requirements

### Requirement: OIDC to CLIs

When a person signs in with OIDC and creates an access token, the server SHALL accept that token for `pulumi up` and `tofu apply`.

#### Scenario: OIDC round trip
- **WHEN** the person signs in through the test OIDC provider and runs both CLIs with the token
- **THEN** both CLIs succeed and the audit log holds the sign-in, the token creation and the writes.

### Requirement: LDAP to CLIs

When a person signs in with LDAP and creates an access token, the server SHALL accept that token for `pulumi up` and `tofu apply`.

#### Scenario: LDAP round trip
- **WHEN** the person signs in through the test directory and runs both CLIs with the token
- **THEN** both CLIs succeed and the audit log holds the entries.

### Requirement: Passkey to CLIs

When a local user with no password signs in with a passkey and creates an access token, the server SHALL accept that token for `pulumi up` and `tofu apply`.

#### Scenario: Passkey round trip
- **WHEN** the local user signs in with a passkey alone and runs both CLIs
- **THEN** both CLIs succeed and the audit log holds the entries.

### Requirement: SAML, CAS, OAuth and header to CLIs

When a person signs in with SAML, CAS, plain OAuth 2.0 or a trusted header and creates an access token, the server SHALL accept that token for `pulumi up` and `tofu apply`.

#### Scenario: Four method round trips
- **WHEN** the person signs in with each of the four methods and runs both CLIs
- **THEN** both CLIs succeed and the audit log holds the entries of each method.
