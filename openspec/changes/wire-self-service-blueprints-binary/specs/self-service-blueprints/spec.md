## ADDED Requirements

### Requirement: The binary serves the blueprint routes

A server that opens a built-in store (SQLite or PostgreSQL) SHALL serve the blueprint routes, for example `GET /api/dustpan/v1/self-service/blueprints/{namespace}`, with no flag.

#### Scenario: The guide's list route on the binary
- **WHEN** a person with a valid token runs the list command of the publish guide against the binary on SQLite or PostgreSQL
- **THEN** the server answers 200 with the blueprints of the namespace, not 404 with `server.route-not-found`
