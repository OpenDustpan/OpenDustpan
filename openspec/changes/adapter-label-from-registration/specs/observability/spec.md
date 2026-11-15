## ADDED Requirements

### Requirement: The adapter label follows the route registration

The server SHALL give each request the adapter label of the adapter that registered its route pattern, from the fixed set `pulumi`, `opentofu`, `inventory` and `server`, and SHALL NOT derive the label from the path.

#### Scenario: A server route under the v1 prefix
- **WHEN** a request reaches a backup route under `/api/dustpan/v1/`
- **THEN** the request metrics and the request log give it the adapter label `server`

#### Scenario: A request that matches no route
- **WHEN** a request matches no registered pattern
- **THEN** the request metrics and the request log give it the adapter label `server`
