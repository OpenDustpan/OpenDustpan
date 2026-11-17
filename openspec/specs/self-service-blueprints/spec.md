# self-service-blueprints Specification

## Purpose
Self-service blueprints let a person publish a named, versioned template and start work from it. This capability covers the publish step of the self-service path in the end-to-end suite. It does not cover the other steps of that path or the blueprint editor.

## Requirements

### Requirement: The self-service persona path publishes a blueprint

The e2e suite SHALL run the publish step of the self-service persona path on every store, with the harness token and the documented routes, and SHALL NOT skip it.

#### Scenario: Priya publishes pg at version 1
- **WHEN** the step writes the draft `pg` with `If-Match: "0"`, publishes the draft version it got, and reads version 1
- **THEN** the answers are 200, 201 and 200, and the version description names the blueprint `pg` and the number 1

### Requirement: The binary serves the blueprint routes

A server that opens a built-in store (SQLite or PostgreSQL) SHALL serve the blueprint routes, for example `GET /api/dustpan/v1/self-service/blueprints/{namespace}`, with no flag.

#### Scenario: The guide's list route on the binary
- **WHEN** a person with a valid token runs the list command of the publish guide against the binary on SQLite or PostgreSQL
- **THEN** the server answers 200 with the blueprints of the namespace, not 404 with `server.route-not-found`
