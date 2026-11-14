## ADDED Requirements

### Requirement: RemovePassword

If a local user holds one live passkey, then the server SHALL accept the RemovePassword call for that user.

#### Scenario: Live passkey
- **WHEN** a user with a password and one live passkey calls RemovePassword
- **THEN** the server deletes the password

#### Scenario: Suspended passkey
- **WHEN** a user with a password and only suspended passkeys calls RemovePassword
- **THEN** the server refuses with ErrLastCredential and keeps the password

#### Scenario: Concurrent last two
- **WHEN** two RemovePassword calls remove the last two credentials at the same time
- **THEN** one call wins and the other RemovePassword call is refused with ErrLastCredential

### Requirement: DeleteUser

If an enabled local user has no password and no live passkey, then the server SHALL refuse the DeleteUser call for that user.

#### Scenario: Live passkey
- **WHEN** an administrator calls DeleteUser for an enabled user with one live passkey
- **THEN** the server deletes the user

#### Scenario: No credential
- **WHEN** an administrator calls DeleteUser for an enabled user with no password and no live passkey
- **THEN** the server refuses with ErrLastCredential and keeps the user

### Requirement: Reset reach cover

When an administrator starts a reset for a local user, the server SHALL check that the reach of the starter covers the reach of the person before it makes a code.

#### Scenario: Reach too small
- **WHEN** the reach of the starter does not cover the person
- **THEN** the server answers 403 with local-user.reset-denied and makes no code

#### Scenario: Store error
- **WHEN** the reach load fails
- **THEN** the server denies with no code and writes an audit entry

### Requirement: Passkey start allowCredentials

When a passkey sign-in starts, the server SHALL return an explicit allowCredentials list.

#### Scenario: Known user
- **WHEN** the sign-in starts for a known user name
- **THEN** the answer holds an explicit allowCredentials list

#### Scenario: Unknown user name
- **WHEN** the sign-in starts with an unknown user name
- **THEN** the answer holds an empty allowCredentials list with the same shape
