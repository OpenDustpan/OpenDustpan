# self-service-blueprints Specification

## Purpose
Self-service blueprints let a person publish a named, versioned template and start work from it. This capability covers the publish step of the self-service path in the end-to-end suite. It does not cover the other steps of that path or the blueprint editor.

## Requirements

### Requirement: The self-service persona path publishes a blueprint

The e2e suite SHALL run the publish step of the self-service persona path on every store, with the harness token and the documented routes, and SHALL NOT skip it.

#### Scenario: Priya publishes pg at version 1
- **WHEN** the step writes the draft `pg` with `If-Match: "0"`, publishes the draft version it got, and reads version 1
- **THEN** the answers are 200, 201 and 200, and the version description names the blueprint `pg` and the number 1
