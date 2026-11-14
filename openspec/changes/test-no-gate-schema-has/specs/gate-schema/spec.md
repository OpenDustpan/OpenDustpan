## ADDED Requirements

### Requirement: no-group-typed-attribute

The server SHALL keep every gate schema entity attribute and every context field free of Group, Set of Group, and entity parent chain that reaches Group.

#### Scenario: guard-fails-on-group-attribute
- **WHEN** a gate schema entity attribute or context field uses Group, Set of Group, or an entity type that reaches Group through its parent chain, including inside nested records, sets, unions, and resolved common types
- **THEN** the guard test fails and names the group-literal rule that holdsGroupLiteral enforces

#### Scenario: guard-passes-on-clean-schemas
- **WHEN** every loaded gate schema has no Group-typed attribute and the loaded set equals the expected ID list
- **THEN** the guard test passes

#### Scenario: guard-fails-on-new-schema
- **WHEN** a gate schema is added without updating the expected ID list
- **THEN** the guard test fails and reports the new schema ID
