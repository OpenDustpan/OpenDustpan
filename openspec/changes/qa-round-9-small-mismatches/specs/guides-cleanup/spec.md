## ADDED Requirements

### Requirement: No internal references

The user guides SHALL state restore, dev reset, registry and namespace behavior with no internal reference.

#### Scenario: Guide lint
- **WHEN** the lint runs over the user guides
- **THEN** make docs-lint passes with no ADR number in the seven named guides

### Requirement: Body limit docs

The restore guide SHALL state that an oversized body keeps the 400 invalid JSON body answer and give wc -c as the size check.

#### Scenario: Oversized body
- **WHEN** an operator reads the guides about request bodies
- **THEN** the guides state the kept 400 answer and the size check

### Requirement: Namespace minimum docs

The quickstart SHALL state the 3-character minimum of a namespace name that the Pulumi CLI needs.

#### Scenario: Short namespace name
- **WHEN** an operator reads the namespace create guide
- **THEN** the guide states the 3-character minimum

### Requirement: Registry keys docs

The registry guide SHALL state that the registry accepts OpenPGP keys only.

#### Scenario: Registry keys
- **WHEN** an operator reads the registry guide
- **THEN** the guide states OpenPGP keys only
