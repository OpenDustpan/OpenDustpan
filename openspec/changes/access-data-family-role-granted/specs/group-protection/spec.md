## ADDED Requirements

### Requirement: Installation-wide access management protects

If a group holds namespace:manage-access, namespace:manage-principals or namespace:manage-principal-tokens on the installation, then the server SHALL report the group as protected.

#### Scenario: Principal management on the installation
- **WHEN** a group holds namespace:manage-principals on the installation
- **THEN** the group reads protected true

#### Scenario: Token management on the installation
- **WHEN** a group holds namespace:manage-principal-tokens on the installation
- **THEN** the group reads protected true

#### Scenario: Access management on the installation
- **WHEN** a group holds namespace:manage-access on the installation
- **THEN** the group reads protected true

#### Scenario: One namespace only
- **WHEN** a group holds namespace:manage-access on one namespace
- **THEN** the group reads protected false

#### Scenario: Read-only role
- **WHEN** a group holds a read-only data role on the installation
- **THEN** the group reads protected false

#### Scenario: Grant ends
- **WHEN** the only grant that protects the group ends
- **THEN** the group reads protected false

### Requirement: One sensitive rule

While it computes the protected flag, the server SHALL read the sensitive set from one helper built on IsSensitive.

#### Scenario: Rule changes
- **WHEN** the sensitive set changes
- **THEN** the flag and the guide table change together

#### Scenario: Unclassified action
- **WHEN** a group holds an action with no family
- **THEN** the group reads protected false

### Requirement: Cause of the flag

When the server reads a protected group, the server SHALL return each cause on an object that the caller can read with the grant ID, the action and the object.

#### Scenario: Grant cause
- **WHEN** a grant on the installation protects the group
- **THEN** the read lists the grant ID, the action and the object

#### Scenario: Act-as cause
- **WHEN** an act-as edge protects the group
- **THEN** the read lists the edge with the principal IDs

#### Scenario: Mixed causes
- **WHEN** a grant and an act-as edge protect the group
- **THEN** the read lists both

### Requirement: Cause limit

When a group has more than 20 readable causes, the server SHALL return 20 causes sorted by grant ID and the count of the other causes.

#### Scenario: Thirty causes
- **WHEN** a group has 30 readable causes
- **THEN** the read lists 20 causes and the hidden count 10

### Requirement: Cause visibility

If the caller cannot read the object of a cause, then the server SHALL count that cause and return no grant ID, action or object for it.

#### Scenario: Unreadable object
- **WHEN** a caller who cannot read the object of a cause reads the group
- **THEN** the read adds 1 to the hidden count and lists nothing of that cause

### Requirement: Read authorization

If the caller lacks the authorization action of the group read, then the server SHALL refuse the read with 403 and return no cause.

#### Scenario: Caller without a grant
- **WHEN** a caller with no grant reads a group
- **THEN** the answer is 403 and holds no cause
