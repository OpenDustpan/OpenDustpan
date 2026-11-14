## ADDED Requirements

### Requirement: Verify chain and SPIFFE ID

When a workload calls the mTLS exchange over direct TLS, the server SHALL accept only a verified chain matching exactly one enabled rule.

#### Scenario: Valid chain and named SPIFFE ID
- **WHEN** A workload presents a chain to an enabled bundle with a live leaf whose URI SAN holds a SPIFFE ID that exactly one enabled rule of the namespace names.
- **THEN** The server issues a stored trust token for the principal of that rule and writes an audit entry for the exchange.

#### Scenario: Wrong CA
- **WHEN** The chain does not lead to an enabled bundle.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Expired leaf
- **WHEN** The leaf is expired or not yet valid at exchange time with no skew allowance.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Revoked leaf
- **WHEN** The bundle revocation source reports the leaf revoked.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Revocation unknown
- **WHEN** The bundle revocation source is unreachable or reports unknown status.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: No client certificate
- **WHEN** The TLS session holds no verified client chain.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: No SPIFFE ID
- **WHEN** The leaf holds no URI SAN with a SPIFFE ID.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Unknown SPIFFE ID
- **WHEN** No enabled rule of the namespace names the SPIFFE ID.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: More than one matching rule
- **WHEN** Two enabled rules of the namespace name the same bundle and SPIFFE ID.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Hostile SPIFFE text
- **WHEN** The leaf holds several URI SANs or a SPIFFE ID that differs in case or trailing text from the rule value.
- **THEN** The server refuses with no token and writes a refusal audit entry.

### Requirement: Issue stored trust token

When the checks pass, the server SHALL issue a stored access token of kind trust with the exchanged-token lifetime.

#### Scenario: Issue with 1-hour expiry
- **WHEN** The checks pass and the principal of the rule is enabled.
- **THEN** The answer holds the token ID, the expiry within the 1-hour setting, and the principal subject.

#### Scenario: Disabled principal
- **WHEN** The principal of the matched rule is disabled.
- **THEN** The server refuses with no token and writes a refusal audit entry.

#### Scenario: Over-long lifetime refused
- **WHEN** The computed expiry exceeds the exchanged-token lifetime or the maximum lifetime.
- **THEN** The server refuses with no token and writes a refusal audit entry.

### Requirement: Atomic match and issue

When the server matches for issue, the server SHALL commit the token with its audit entry in one transaction.

#### Scenario: Store failure
- **WHEN** The issue transaction fails.
- **THEN** No token exists and no issue audit entry exists, with a refusal audit entry.

#### Scenario: Concurrent disable
- **WHEN** A rule, bundle, or principal is disabled between match and commit.
- **THEN** The transaction aborts with no token and a refusal audit entry.

### Requirement: Enforce grant reach and limits

While a trust token is live, the server SHALL deny each use outside grant reach under access limits.

#### Scenario: Use within grants
- **WHEN** A caller uses the trust token on a later request.
- **THEN** The server allows only actions that grants and access limits allow for that principal.

### Requirement: End use at revoke or disable

If a trust token loses standing, then the server SHALL deny its later use.

#### Scenario: Revoke ends use
- **WHEN** A holder of token manage rights revokes the trust token through the service-token revoke route.
- **THEN** The next use gives 401.

#### Scenario: Principal disabled after issue
- **WHEN** The principal of a live trust token is disabled.
- **THEN** The next use gives 401.

#### Scenario: Rule disabled after issue
- **WHEN** The matched rule or bundle is disabled after issue.
- **THEN** The live token works until expiry or revoke and a new exchange with the same SPIFFE ID refuses.
