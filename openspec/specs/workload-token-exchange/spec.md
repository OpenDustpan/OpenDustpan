# workload-token-exchange Specification

## Purpose
This capability covers the token exchange of a workload. When a trust rule accepts the JWT of a workload, the server issues a stored access token of the kind `jwt-exchange` for the principal of the rule. A request with that token has the credential `trust-token`, and the token list and the audit entry show the kind.

## Requirements

### Requirement: The JWT exchange issues a token of the kind jwt-exchange
The server SHALL issue a stored access token of the kind `jwt-exchange` when a
trust rule accepts the JWT of a workload. A request with that token SHALL have
the credential `trust-token`. The token list of the principal and the audit
entry of the issue SHALL show the kind.

#### Scenario: Exchange a JWT
- **GIVEN** an enabled trust rule that accepts the JWT of a workload
- **WHEN** the workload calls the token exchange with the JWT
- **THEN** the server issues a stored token of the kind `jwt-exchange` for the principal of the rule, and a request with that token has the credential `trust-token`

#### Scenario: No kind trust
- **GIVEN** the token list of a service principal that got tokens from the JWT exchange
- **WHEN** a site administrator lists the tokens of the principal
- **THEN** each of those tokens shows the kind `jwt-exchange`, and no token shows the kind `trust`
