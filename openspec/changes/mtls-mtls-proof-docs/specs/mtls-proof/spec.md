## ADDED Requirements

### Requirement: prove-mtls-with-real-clis

When a workload presents a valid mTLS client certificate at the exchange, the server SHALL issue the stored trust token that runs the Pulumi CLI and OpenTofu.

#### Scenario: Valid SPIFFE ID runs both CLIs
- **WHEN** a workload presents a client certificate from the trust bundle whose URI SAN holds a SPIFFE ID that a trust rule of the namespace names
- **THEN** pulumi up and tofu apply pass with the exchanged trust token and the audit log holds one exchange entry.

#### Scenario: Foreign CA gets no token
- **WHEN** the client certificate chains to another CA
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Expired certificate gets no token
- **WHEN** the client certificate is past its expiry
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Unmatched ID gets no token
- **WHEN** the SPIFFE ID matches no trust rule of the namespace
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: No certificate behind proxy gets no token
- **WHEN** the exchange sees no client certificate from the TLS handshake
- **THEN** the server answers 401 with a body that names the proxy rule, issues no token, and writes a refused exchange audit entry.

### Requirement: refuse-hostile-certificates

When a client certificate holds more than one URI SAN or a SPIFFE ID outside the matched trust rule, the server SHALL refuse the exchange with 401.

#### Scenario: Two URI SANs refused
- **WHEN** the client certificate holds two URI SANs
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Other trust domain refused
- **WHEN** the SPIFFE ID names another trust domain than the bundle
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Forwarded header alone refused
- **WHEN** the request sends a forwarded certificate header with no handshake certificate
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Refusal body names the match
- **WHEN** any hostile certificate in this requirement is presented
- **THEN** the 401 body names the failed match and the rule to check.

### Requirement: use-token-within-grants

When a workload uses an exchanged trust token on a unit, the server SHALL enforce the grants of the principal of the rule.

#### Scenario: Token without grant is refused
- **WHEN** the principal of the trust token holds no grant on the unit
- **THEN** pulumi up and tofu apply answer 403 and change no state.

#### Scenario: Rule of one namespace reaches no other namespace
- **WHEN** the trust token from a rule of one namespace is used in another namespace
- **THEN** the CLIs answer 403 in the other namespace and change no state.

### Requirement: prove-token-lifetime

When a trust token expires or is revoked, the server SHALL refuse its use with 401.

#### Scenario: Expired trust token fails
- **WHEN** the trust token is past its 1-hour expiry
- **THEN** the CLI call answers 401 and changes no state.

#### Scenario: Revoked trust token fails
- **WHEN** the trust token was revoked through the service token routes
- **THEN** the CLI call answers 401 and changes no state.

#### Scenario: Rule removal stops new exchanges only
- **WHEN** the trust rule or bundle is removed after the exchange
- **THEN** a new exchange answers 401 while the live token runs until expiry or revoke.

### Requirement: mtls-docs-page

The server SHALL ship a docs page that shows how to set the trust bundle, add the trust rule, run the exchange with a client certificate, use the token with both CLIs, and place mTLS behind a TLS proxy.

#### Scenario: Docs runbook runs as written
- **WHEN** an operator follows the mTLS docs page on a new install
- **THEN** the exchange returns a trust token and a CLI read passes.

#### Scenario: Docs shows safe token transport
- **WHEN** an operator follows the token transport block of the docs page
- **THEN** the token reaches the CLIs with no flag or log line that holds it.
