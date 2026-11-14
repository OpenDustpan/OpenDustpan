## ADDED Requirements

### Requirement: Collection writer scope

When a principal with a writer grant on one collection writes a unit of another collection, the server SHALL answer 403.

#### Scenario: Write outside the granted collection
- **WHEN** a principal with writer on collection A runs pulumi up on a unit of collection B
- **THEN** the server answers 403 and changes nothing

### Requirement: Viewer refusal

When a principal with a viewer grant starts a write update, the server SHALL answer 403.

#### Scenario: Viewer writes
- **WHEN** a viewer runs tofu apply
- **THEN** the server answers 403 and changes nothing

#### Scenario: Viewer reads
- **WHEN** a viewer runs pulumi stack export
- **THEN** the server answers the state

### Requirement: Revocation timing

When a grant ends, the server SHALL refuse the next request that needs that grant.

#### Scenario: Use after revocation
- **WHEN** the grant ends and the same token sends the next CLI request
- **THEN** the server answers 403

### Requirement: Access-review export

When a caller with the read-access action asks for the access-review export, the server SHALL answer each grant with holder, role, object, creator and time.

#### Scenario: Export
- **WHEN** an allowed caller asks for the export
- **THEN** each row holds holder, role, object, creator and time
