## ADDED Requirements

### Requirement: Exchange path

Where a workload exchanges an mTLS certificate, the server SHALL serve the exchange at POST /trust/v1/namespaces/{namespace}/exchange-mtls.

#### Scenario: Exchange outside tool-neutral API
- **WHEN** A workload calls POST /trust/v1/namespaces/{namespace}/exchange-mtls with a client certificate
- **THEN** The exchange answers at that path and the trust rule and bundle routes stay under /api/dustpan/v1.

#### Scenario: Proxied request with no certificate
- **WHEN** A request reaches the exchange path with no client certificate because a TLS proxy ended TLS
- **THEN** The server answers 401 and issues no token, and reads no proxy header for identity.

### Requirement: Exchange valid certificate for trust token

When a workload presents a valid client certificate from the configured trust bundle, the server SHALL match one trust rule of the request namespace on the SPIFFE ID and issue a stored trust token for the principal of that rule.

#### Scenario: Valid certificate with named SPIFFE ID
- **WHEN** A workload presents a client certificate from the trust bundle with one SPIFFE ID in the URI SAN that one trust rule of the namespace names
- **THEN** The server issues a stored access token of kind trust for the principal of the rule and writes an exchange audit entry.

#### Scenario: Unknown SPIFFE ID
- **WHEN** The SPIFFE ID matches no trust rule of the namespace
- **THEN** The server answers 401 and issues no token.

#### Scenario: Wrong CA or expired certificate
- **WHEN** The chain comes from another CA or the certificate is expired
- **THEN** The server answers 401 and issues no token.

### Requirement: Exchange client

When a workload exchanges a client certificate, the server SHALL accept the certificate from the TLS handshake of a generic HTTPS client.

#### Scenario: Exchange with generic HTTPS client
- **WHEN** A generic HTTPS client presents the client certificate at the exchange path
- **THEN** The server issues the trust token and the tool CLIs use that bearer token for later calls.

### Requirement: Chain checks

When the client chain is revoked or lacks client authentication purpose, the server SHALL refuse the exchange with 401 and issue no token.

#### Scenario: Revoked certificate
- **WHEN** The certificate serial is on the stored deny list of the bundle
- **THEN** The server answers 401 and issues no token.

#### Scenario: Wrong purpose
- **WHEN** The certificate lacks client authentication extended key usage
- **THEN** The server answers 401 and issues no token.

### Requirement: Hostile chain

When the presented chain is oversized or a URI SAN uses a scheme other than spiffe, the server SHALL refuse the exchange with 401.

#### Scenario: Oversized chain
- **WHEN** The presented chain is larger than the server limit
- **THEN** The server answers 401 and issues no token.

#### Scenario: Wrong URI scheme
- **WHEN** A URI SAN uses a scheme other than spiffe
- **THEN** The server answers 401 and issues no token.

### Requirement: Revocation state

When the server cannot read the stored revocation state of the trust bundle, the server SHALL fail closed with 401.

#### Scenario: Missing revocation state
- **WHEN** The stored revocation state of the bundle is missing at exchange time
- **THEN** The server answers 401 and issues no token.

### Requirement: Revocation manage

When a person adds a serial to the deny list of a trust bundle, the server SHALL refuse each later exchange with that serial.

#### Scenario: Added serial refused
- **WHEN** A person adds a serial to the deny list and the workload exchanges again
- **THEN** The server answers 401 and the audit export reports the serial and the bundle.

### Requirement: Time checks

When the current time is outside the validity period of the client certificate, the server SHALL refuse the exchange with 401.

#### Scenario: Future certificate
- **WHEN** The current time is before the not-before time of the certificate
- **THEN** The server answers 401 and issues no token.

### Requirement: Ambiguous identity

When the certificate identity is ambiguous, the server SHALL refuse the exchange with 401.

#### Scenario: Duplicate rules
- **WHEN** Two enabled trust rules of one namespace name the same SPIFFE ID
- **THEN** The server answers 401 and issues no token.

#### Scenario: Several or zero SANs
- **WHEN** The certificate holds several URI SANs or no URI SAN
- **THEN** The server answers 401 and issues no token.

### Requirement: Enabled rule only

When the matching trust rule or trust bundle is disabled, the server SHALL refuse the exchange with 401.

#### Scenario: Disabled rule
- **WHEN** The only rule that names the SPIFFE ID is disabled
- **THEN** The server answers 401 and issues no token.

#### Scenario: Disabled bundle
- **WHEN** The trust bundle of the namespace is disabled
- **THEN** The server answers 401 and issues no token.

### Requirement: Bundle save checks

When a person saves a malformed, empty or anchor-less trust bundle, the server SHALL refuse the save with 400.

#### Scenario: Malformed bundle
- **WHEN** A person saves a trust bundle that is malformed, empty or holds no trust anchor
- **THEN** The server answers 400 and stores no bundle.

### Requirement: Bundle rotation

When a person rotates a trust bundle, the server SHALL accept an overlap of two stored bundles and refuse chains from a removed bundle.

#### Scenario: Overlap of two bundles
- **WHEN** A person adds a second trust bundle before the old bundle expires
- **THEN** Exchanges with chains from either bundle pass until the old bundle is removed.

#### Scenario: Bundle removed
- **WHEN** A person removes a trust bundle while exchanges run
- **THEN** The server answers 401 for chains from the removed bundle.

### Requirement: Namespace fence of trust rule

If a trust rule belongs to another namespace, then the server SHALL refuse the exchange.

#### Scenario: Cross-namespace rule
- **WHEN** A trust rule of another namespace names the same SPIFFE ID
- **THEN** The server answers 401 and issues no token.

### Requirement: Atomic check and issue

When the server exchanges a certificate, the server SHALL read the trust rule set and issue the token in one transaction snapshot.

#### Scenario: Rule removed during exchange
- **WHEN** An administrator removes the trust rule after the chain check and before token issue
- **THEN** The server answers 401 and issues no token.

### Requirement: Trust token lifetime and revoke

When the server issues a trust token through mTLS, the server SHALL give it the exchanged workload token lifetime and the service token revoke path.

#### Scenario: Lifetime
- **WHEN** The server issues a trust token through mTLS
- **THEN** The token expires after the exchanged workload token lifetime of 1 hour by default, capped by the shorter namespace maximum, and the revoke route ends it at once.

### Requirement: Token after rule change

When an administrator changes or removes the trust rule or trust bundle, the server SHALL keep each issued token valid until its expiry or revoke.

#### Scenario: Rule removed
- **WHEN** An administrator removes or changes the trust rule or trust bundle while an issued token is still valid
- **THEN** The token stays valid until expiry or revoke.

### Requirement: No fresh authentication at exchange

When a workload exchanges a valid certificate, the server SHALL require no fresh authentication and no grant for the exchange itself.

#### Scenario: Machine exchange
- **WHEN** A workload presents a valid certificate that one rule names
- **THEN** The rule match alone decides, with no grant check and no fresh authentication.

### Requirement: Limits hold for trust token

When an access limit forbids an action, the server SHALL refuse that action for a trust token that mTLS issued.

#### Scenario: Limited action
- **WHEN** An access limit forbids the action that a trust token requests
- **THEN** The server answers 403.

### Requirement: Use inside grants

When a trust token requests an object outside the grants of its principal, the server SHALL refuse with 403.

#### Scenario: Outside reach
- **WHEN** A trust token of one namespace requests a unit of another namespace or a collection its principal cannot use
- **THEN** The server answers 403 with code namespace.denied.

### Requirement: Trust rule and bundle auth

When a caller requests a trust rule or trust bundle change, the server SHALL require action namespace:manage-trust-rules on the namespace.

#### Scenario: Without action
- **WHEN** A caller without namespace:manage-trust-rules on the namespace requests a trust rule or trust bundle change
- **THEN** The server answers 403 and makes no rule or bundle.

#### Scenario: Without reach
- **WHEN** A granter whose reach misses the target principal grants a role with namespace:manage-trust-rules
- **THEN** The server answers 403 and makes no grant.

### Requirement: Fresh authentication for rule change

When a person changes a trust rule or a trust bundle, the server SHALL require a fresh authentication within freshAuthSeconds.

#### Scenario: Stale authentication
- **WHEN** The authentication age is past freshAuthSeconds at a trust rule or trust bundle change
- **THEN** The server answers 401 and makes no change.

### Requirement: Audit of exchange and rule change

When a workload exchanges a certificate or a person changes a trust rule or trust bundle, the server SHALL write an audit entry for the exchange or the change.

#### Scenario: Export over time range
- **WHEN** Workloads exchange certificates and an administrator changes a trust rule and a trust bundle
- **THEN** The export holds the issued entry with certificate fingerprint, the refused entry, the rule change entry and the bundle change entry for that range.
