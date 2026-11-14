## ADDED Requirements

### Requirement: Save namespace proxy rule set

When a principal with `namespace:manage-proxy-rules` saves a valid proxy rule set, the server SHALL store the set for the namespace.

#### Scenario: Save valid namespace set
- **WHEN** a principal with namespace manage proxy rules saves a valid set with fresh authentication
- **THEN** the server stores the set, writes an audit entry with the old and the new digest, and returns the new digest

#### Scenario: Read one namespace scope
- **WHEN** a reader with namespace read access reads the namespace set
- **THEN** the server returns the stored namespace set and its digest

#### Scenario: Refuse namespace read without grant
- **WHEN** a reader without namespace read access reads the namespace set
- **THEN** the server refuses the read with access denied

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

#### Scenario: Read one unit scope
- **WHEN** a reader with unit read access reads the unit set
- **THEN** the server returns the stored unit set and its digest

#### Scenario: Refuse unit save without fresh authentication
- **WHEN** a principal with unit manage proxy rules saves a unit set without fresh authentication
- **THEN** the server refuses the save and asks for fresh authentication

### Requirement: Refuse unit save without grant

When a principal without `unit:manage-proxy-rules` saves a unit set, the server SHALL refuse the save.

#### Scenario: Refuse unit save without grant
- **WHEN** a principal without unit manage proxy rules saves a unit set
- **THEN** the server refuses the save with access denied

### Requirement: Refuse stale digest

When a save sends an If-Match digest that is not the stored digest, the server SHALL refuse the save.

#### Scenario: Refuse save with stale digest
- **WHEN** a save sends an If-Match digest that is not the stored digest
- **THEN** the server answers 412 and changes nothing

#### Scenario: Accept save with current digest
- **WHEN** a save sends an If-Match digest that equals the stored digest
- **THEN** the server stores the set and returns the new digest

### Requirement: Reject invalid proxy rule

When a save holds an invalid proxy rule, the server SHALL refuse the save and name the proxy rule id and the field.

#### Scenario: Reject bad host and path
- **WHEN** a save holds a bad host, port, method, path pattern, or operation
- **THEN** the server refuses the save and names the proxy rule id and the field

#### Scenario: Reject bad id and oversize document
- **WHEN** a save holds a bad id, a duplicate id in one set, an unknown field, or a document past the size limit
- **THEN** the server refuses the save and names the proxy rule id and the field

#### Scenario: Reject empty or unknown operations
- **WHEN** a proxy rule holds an empty operations list or an unknown operation
- **THEN** the server refuses the save and names the proxy rule id and the operations field

#### Scenario: Reject IP host and wildcard host
- **WHEN** a proxy rule holds an IP literal, a trailing dot, a wildcard star, an uppercase name, or a non ASCII name
- **THEN** the server refuses the save and names the proxy rule id and the host field

#### Scenario: Reject too many patterns
- **WHEN** a save holds more than 200 path patterns
- **THEN** the server refuses the save and names the proxy rule id

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
- **THEN** the server returns the joined proxy rules for plan with stored patterns, rule fields only, and no template render

#### Scenario: Refuse rendered view without read access
- **WHEN** a reader without unit read access requests the rendered view
- **THEN** the server refuses the request with access denied

#### Scenario: Refuse rendered view with bad operation
- **WHEN** a reader requests the rendered view with an unknown operation
- **THEN** the server refuses the request and names the operation field

### Requirement: Handle unknown scope and sequential saves

When two saves with no digest reach the same scope in sequence, the server SHALL keep the last write.

#### Scenario: Read unknown scope
- **WHEN** a reader reads the proxy rule set of a namespace or unit that does not exist
- **THEN** the server returns not found

#### Scenario: Save unknown scope
- **WHEN** a principal saves a proxy rule set to a namespace or unit that does not exist
- **THEN** the server returns not found

#### Scenario: Sequential saves to one scope
- **WHEN** two saves with no digest reach the same scope in sequence
- **THEN** the server keeps the last write and writes an audit entry for each save

#### Scenario: Read missing set
- **WHEN** a reader reads a scope that exists but holds no set
- **THEN** the server returns an empty set with no digest
