## ADDED Requirements

### Requirement: namespace-key-double-wrap

When a namespace has a customer key, the server SHALL wrap each namespace key version first with the customer key through the KMS and then wrap the result with the key-encryption key.

#### Scenario: Double wrap opens only with both keys
- **WHEN** The server wraps a namespace key version for a namespace with a customer key.
- **THEN** Unwrap with the same namespace ID opens the 32-byte version, and Unwrap with another namespace ID fails.

#### Scenario: Hostile KMS answer fails closed
- **WHEN** The KMS answers a wrong key version, a short or oversize unwrapped key, a 5xx, or a revoked-key error.
- **THEN** Unwrap answers ErrUnavailable or ErrRevoked and writes an audit entry with no open key.

### Requirement: namespace-key-builtin-wrap

When a namespace has no customer key, the server SHALL wrap each namespace key version with the key-encryption key only.

#### Scenario: No customer key uses one wrap
- **WHEN** The server wraps a namespace key version for a namespace with no customer key.
- **THEN** The record holds one wrap with the wrapper ID of the key-encryption key and Unwrap opens it.

### Requirement: wrapper-namespace-identity

When the server calls Wrap or Unwrap, the server SHALL send the namespace ID, the namespace key ID, and the purpose as KMS context with the identity of that namespace only.

#### Scenario: Cross-namespace replay fails
- **WHEN** The server calls Wrap or Unwrap for two namespaces with swapped contexts.
- **THEN** The KMS call carries the context of the calling namespace and Unwrap for the other namespace fails.

#### Scenario: Concurrent wrap of same version
- **WHEN** Two callers wrap the same namespace key version at the same time.
- **THEN** One wrap wins and the other answers a conflict with no second record for the same version.

### Requirement: confused-deputy-probe

When the server sets a customer key, the server SHALL run ProbeConfusedDeputy with the namespace identity and refuse use when the probe fails.

#### Scenario: Probe refuses broad policy
- **WHEN** The confused-deputy probe passes for a random ID or otherwise fails.
- **THEN** The server refuses the customer key and writes an audit entry in the scope of the namespace.

### Requirement: startup-wrapper-check

If a stored namespace key version records a wrapper ID that does not match, then the server SHALL refuse to start.

#### Scenario: Wrong wrapper refuses start
- **WHEN** A stored namespace key version records a wrapper ID that does not match the key-encryption key.
- **THEN** The server refuses to start and names the key variable.
