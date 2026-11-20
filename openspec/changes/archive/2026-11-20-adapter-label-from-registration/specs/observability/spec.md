## ADDED Requirements

### Requirement: The adapter label follows the route registration

The server SHALL give each request the adapter label of the adapter that registered its route pattern, from the fixed set `pulumi`, `opentofu`, `inventory` and `server`, and SHALL NOT derive the label from the path.

#### Scenario: A server route under the v1 prefix
- **WHEN** a request reaches a backup route under `/api/dustpan/v1/`
- **THEN** the request metrics and the request log give it the adapter label `server`

#### Scenario: A request that matches no route
- **WHEN** a request matches no registered pattern
- **THEN** the request metrics and the request log give it the adapter label `server`

### Requirement: The in-flight gauge and the request counter share one adapter label

The server SHALL give the in-flight gauge and the request counter the same adapter label for each request. The server SHALL take the label of the gauge from the root mux pattern before the handler runs, from the same label map as the counter. The label map SHALL hold the path of each source pattern with the method removed.

#### Scenario: A v1 route of each adapter
- **WHEN** a request reaches one v1 route of an adapter
- **THEN** the in-flight gauge and the request counter give it the same adapter label

### Requirement: A path with two different adapter labels stops the server

The server SHALL NOT start if two adapters register the same path with different adapter labels. The server SHALL NOT pick one of the labels by a rule.

#### Scenario: A duplicate path with different labels
- **WHEN** two adapters register the same path with different adapter labels
- **THEN** building the label map fails with an error that names the path and both adapters, and the server does not start

#### Scenario: A duplicate path with the same label
- **WHEN** two adapters register the same path with the same adapter label
- **THEN** building the label map succeeds
