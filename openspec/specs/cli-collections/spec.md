# cli-collections Specification

## Purpose
TBD - created by archiving change cli-collections-and-placement-commands. Update Purpose after archive.

## Requirements

### Requirement: CLI commands for collections and placement rules

The `opendustpan` CLI SHALL create and list collections and add, list and delete placement rules through the existing routes, with the admin token of `namespace create`.

#### Scenario: Create a collection
- **WHEN** an engineer runs `opendustpan collection create acme/prod` with an admin token
- **THEN** the CLI calls the collections route of the namespace and prints the stable ID of the new collection

#### Scenario: A rule names an unknown collection
- **WHEN** an engineer runs `opendustpan placement add` with a collection name that the namespace does not have
- **THEN** the CLI makes no rule and prints that the collection does not exist and the command `opendustpan collection list`

#### Scenario: No rule matches
- **WHEN** a new unit matches no placement rule
- **THEN** the 422 answer `placement.no-match` names `opendustpan placement add`
