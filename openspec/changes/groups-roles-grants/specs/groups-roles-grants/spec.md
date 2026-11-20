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

### Requirement: Namespace discovery for grant holders

When a principal holds a grant on a collection or a unit in a namespace, the organization and discovery routes of that namespace SHALL answer the principal, as ADR 374 decision 1 says.

#### Scenario: Holder discovers the namespace
- **WHEN** a principal with a grant on a collection reads the entitlement-set of the namespace
- **THEN** the server answers that the namespace exists with its entitlements and names the grant that allowed it

#### Scenario: List stays filtered
- **WHEN** a principal with a grant on collection A lists the workspaces of the namespace
- **THEN** the server shows no unit of another collection

#### Scenario: No grant in the namespace
- **WHEN** a principal with no grant in the namespace reads the organization
- **THEN** the server answers the same 404 with the same body as for an unknown namespace

#### Scenario: Revoked grant
- **WHEN** the grant ends and the same principal sends the next discovery request
- **THEN** the server answers 404

#### Scenario: Unit grant without namespace read in the user route
- **WHEN** a principal holds a unit-read grant in the namespace but no `namespace:read` grant, and reads `/api/user`
- **THEN** the server does not list the namespace in the answer

### Requirement: Access-review export

When a caller with the read-access action asks for the access-review export, the server SHALL answer each grant with holder, role, object, creator and time.

#### Scenario: Export
- **WHEN** an allowed caller asks for the export
- **THEN** each row holds holder, role, object, creator and time
