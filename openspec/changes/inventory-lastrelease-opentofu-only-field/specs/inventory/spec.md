## ADDED Requirements

### Requirement: No last release in unit view

The server SHALL omit lastRelease from each inventory unit view.

#### Scenario: OpenTofu unit
- **WHEN** a caller reads a workspace from the inventory
- **THEN** the unit view holds no lastRelease field

#### Scenario: Pulumi unit
- **WHEN** a caller reads a stack from the inventory
- **THEN** the unit view holds no lastRelease field
