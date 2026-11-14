## ADDED Requirements

### Requirement: Verify chain and SPIFFE ID

When a workload calls the mTLS exchange over direct TLS, the server SHALL accept only a verified chain that matches exactly one enabled rule.

#### Scenario: Valid chain
- **WHEN** A leaf with a SPIFFE ID in the URI SAN that one enabled rule names chains to an enabled bundle.
- **THEN** The server issues a trust token and writes an audit entry.

#### Scenario: Wrong CA
- **WHEN** The chain does not lead to an enabled bundle.
- **THEN** The server refuses with 401 and no token.

#### Scenario: Expired leaf
- **WHEN** The leaf is past its not-after time.
- **THEN** The server refuses with 401 and no token.

#### Scenario: Revoked or unknown
- **WHEN** The revocation source reports revoked, or is unreachable.
- **THEN** The server refuses with 401 and no token.

#### Scenario: No match
- **WHEN** No enabled rule names the SPIFFE ID, or two rules do.
- **THEN** The server refuses with 401 and no token.

### Requirement: Issue stored trust token

When the checks pass, the server SHALL issue a stored access token with the exchanged-token lifetime.

#### Scenario: Capped expiry
- **WHEN** The checks pass with the default setting.
- **THEN** The expiry is at most 1 hour from issue and at most the leaf not-after time.

### Requirement: Atomic issue

When the server issues a token, the server SHALL commit the token with its audit entry in one transaction.

#### Scenario: Store failure
- **WHEN** The transaction fails.
- **THEN** No token and no issue audit entry exist, and the answer is 500.

### Requirement: End use

If a trust token is revoked or its principal is disabled, then the server SHALL deny its later use.

#### Scenario: Revoke
- **WHEN** A holder revokes the token.
- **THEN** The next use gives 401.
