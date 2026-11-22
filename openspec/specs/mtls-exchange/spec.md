# mtls-exchange Specification

## Purpose
Define how a workload exchanges its client certificate for a short-lived trust token over direct TLS. The server accepts only a chain that leads to the active version of the trust bundle of the namespace and matches exactly one rule by exact SPIFFE ID. It stores the token of the kind `mtls-exchange` and its audit entry in one transaction, checks again at the record step that the bundle version is still active, and refuses a wildcard in a SPIFFE ID pattern.

## Requirements

### Requirement: Verify chain and SPIFFE ID

When a workload calls the mTLS exchange over direct TLS, the server SHALL accept only a verified chain that matches exactly one enabled rule.

#### Scenario: Valid chain
- **WHEN** A leaf with a SPIFFE ID in the URI SAN that one enabled rule names chains to the active version of the bundle of the namespace.
- **THEN** The server issues a trust token and writes an audit entry.

#### Scenario: Wrong CA
- **WHEN** The chain does not lead to the active version of the bundle of the namespace.
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

When the checks pass, the server SHALL issue a stored access token of the kind `mtls-exchange` with the exchanged-token lifetime.

#### Scenario: Kind of the token
- **WHEN** The checks pass.
- **THEN** The token has the kind `mtls-exchange`, the token list of the principal shows that kind, the issue audit entry records it, and a request with the token has the credential `trust-token`.

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

### Requirement: Recheck the bundle version at the record step

When the server stores an mTLS trust token, the server SHALL check in the same transaction that the bundle version that verified the chain is still the active version of the bundle of the rule in the namespace.

#### Scenario: Version still active
- **WHEN** The active version of the bundle is the version that verified the chain.
- **THEN** The server stores the token and its audit entry.

#### Scenario: Bundle rotated after the verify step
- **WHEN** A rotation made another version active after the chain was verified and before the token is stored.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

#### Scenario: Version not stored
- **WHEN** The version that verified the chain is no longer stored in the bundle.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

#### Scenario: Bundle of another namespace
- **WHEN** The bundle or the version is the active bundle or version of another namespace.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

### Requirement: Exact SPIFFE ID in a rule

When a caller creates or updates a trust rule with a `spiffePattern`, the server SHALL refuse a pattern that has a `*` and name the field and the need for an exact SPIFFE ID in the answer.

#### Scenario: Exact SPIFFE ID
- **WHEN** The rule has a `spiffePattern` with no `*`.
- **THEN** The server stores the rule as before.

#### Scenario: Wildcard at create
- **WHEN** A create names a `spiffePattern` that ends in `/*`.
- **THEN** The server refuses with 400 and the error code trust-rule.spiffe-wildcard, the message names `spiffePattern` and says that the rule must name an exact SPIFFE ID, and the server stores nothing and writes no audit entry.

#### Scenario: Wildcard at update
- **WHEN** An update sets `spiffePattern` to a value that ends in `/*`.
- **THEN** The server refuses with 400 and the error code trust-rule.spiffe-wildcard, and the stored rule and its revision stay as they were.

#### Scenario: Rule with no SPIFFE ID
- **WHEN** A rule has an empty `spiffePattern`.
- **THEN** The server handles the rule as before, and a JWT exchange of the rule is unchanged.
