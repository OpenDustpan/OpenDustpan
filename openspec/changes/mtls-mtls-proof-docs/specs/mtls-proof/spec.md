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
- **THEN** the server answers 401, issues no token, writes a refused exchange audit entry, and the body names the proxy rule.

### Requirement: refuse-hostile-certificates

When a client certificate holds more than one URI SAN or a SPIFFE ID outside the matched trust rule, the server SHALL refuse the exchange with 401 and issue no token.

#### Scenario: Two URI SANs refused
- **WHEN** the client certificate holds two URI SANs
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Other trust domain refused
- **WHEN** the SPIFFE ID names another trust domain than the bundle
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Wrong SPIFFE path refused
- **WHEN** the SPIFFE ID path matches no trust rule of the namespace
- **THEN** the server answers 401, issues no token, and writes a refused exchange audit entry.

#### Scenario: Refusal body names the match
- **WHEN** any hostile certificate in this requirement is presented
- **THEN** the 401 body names the failed match and the rule to check.

### Requirement: mtls-docs-page

The server SHALL ship a docs page that shows how to set the trust bundle, add the trust rule, run the exchange with a client certificate, use the token with both CLIs, and place mTLS behind a TLS proxy.

#### Scenario: Docs runbook runs as written
- **WHEN** an operator follows the mTLS docs page on a new install
- **THEN** the exchange returns a trust token and a CLI read passes.
