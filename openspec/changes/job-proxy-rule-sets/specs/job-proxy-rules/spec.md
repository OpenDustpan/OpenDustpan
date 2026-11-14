## ADDED Requirements

### Requirement: Save namespace proxy rule set

When a principal with `namespace:manage-proxy-rules` saves a valid proxy rule set, the server SHALL store the set for the namespace.

#### Scenario: Save valid namespace set
- **WHEN** a principal with namespace manage proxy rules saves a valid set with fresh authentication
- **THEN** the server stores the set, writes an audit entry with the old and the new digest, and returns the new digest

### Requirement: Refuse save without grant

When a principal without `namespace:manage-proxy-rules` saves a namespace set, the server SHALL refuse the save.

#### Scenario: Refuse namespace save without grant
- **WHEN** a principal without namespace manage proxy rules saves a namespace set
- **THEN** the server refuses the save with access denied

### Requirement: Refuse save without fresh authentication

When a principal saves a namespace set without fresh authentication, the server SHALL refuse the save.

#### Scenario: Refuse namespace save without fresh authentication
- **WHEN** a principal saves a namespace set without fresh authentication
- **THEN** the server refuses the save and asks for fresh authentication

### Requirement: Save unit proxy rule set

When a principal with `unit:manage-proxy-rules` saves a valid proxy rule set, the server SHALL store the set for the unit.

#### Scenario: Save valid unit set
- **WHEN** a principal with unit manage proxy rules saves a valid set with fresh authentication
- **THEN** the server stores the set, writes an audit entry with the old and the new digest, and returns the new digest

### Requirement: Refuse unit save without grant or fresh authentication

When a principal without `unit:manage-proxy-rules` saves a unit set, the server SHALL refuse the save.

#### Scenario: Refuse unit save without grant
- **WHEN** a principal without unit manage proxy rules saves a unit set
- **THEN** the server refuses the save with access denied

### Requirement: Reject invalid proxy rule

When a save holds an invalid proxy rule, the server SHALL refuse the save and name the proxy rule id and the field.

#### Scenario: Reject bad host and path
- **WHEN** a save holds a bad host, port, method, path pattern, or operation
- **THEN** the server refuses the save and names the proxy rule id and the field

#### Scenario: Reject bad id and oversize document
- **WHEN** a save holds a bad id, a duplicate id in one set, an unknown field, or a document past the size limit
- **THEN** the server refuses the save and names the proxy rule id and the field

### Requirement: Join scopes for a unit

When a reader reads the proxy rule set of a unit, the server SHALL return the joined namespace set and unit set in fixed order.

#### Scenario: Read joined set
- **WHEN** a reader reads the joined proxy rule set of a unit
- **THEN** the server returns the namespace proxy rules followed by the unit proxy rules with scope, operation, host, port, method, and canonical path match

#### Scenario: Read empty joined set
- **WHEN** neither the namespace nor the unit holds a set
- **THEN** the server returns no proxy rules and the unit keeps current behavior

### Requirement: Rendered debug view

When a reader with unit read access requests the rendered view for one operation, the server SHALL return the joined proxy rules for that operation.

#### Scenario: Render plan view
- **WHEN** a reader with unit read access requests the rendered view for operation plan
- **THEN** the server returns the joined proxy rules for plan with stored patterns and no secret values

#### Scenario: Refuse rendered view without read access
- **WHEN** a reader without unit read access requests the rendered view
- **THEN** the server refuses the request with access denied

### Requirement: Handle unknown scope and sequential saves

When two saves reach the same scope in sequence, the server SHALL keep the last write.

#### Scenario: Read unknown scope
- **WHEN** a reader reads the proxy rule set of a namespace or unit that does not exist
- **THEN** the server returns not found

#### Scenario: Sequential saves to one scope
- **WHEN** two saves reach the same scope in sequence
- **THEN** the server keeps the last write and writes an audit entry for each save
