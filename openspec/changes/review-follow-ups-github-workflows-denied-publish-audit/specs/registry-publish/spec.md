## ADDED Requirements

### Requirement: Denied module publish audit entry

When the server refuses a module archive publish, the server SHALL write one denied entry to the namespace audit log.

#### Scenario: Denied module publish
- **WHEN** the server refuses a module archive publish
- **THEN** The namespace audit log holds one denied entry with actor, action, object and refusal reason, and no archive bytes.

### Requirement: Denied provider publish audit entry

When the server refuses a provider archive publish, the server SHALL write one denied entry to the namespace audit log.

#### Scenario: Denied provider publish
- **WHEN** the server refuses a provider archive publish
- **THEN** The namespace audit log holds one denied entry with actor, action, object and refusal reason, and no archive bytes.

### Requirement: Denied publish stores nothing

When the server refuses a module or provider archive publish, the server SHALL store no release record and no content blob for the refused version.

#### Scenario: Refused archive stores nothing
- **WHEN** the server refuses a module or provider archive publish
- **THEN** No release record and no content blob exist for the refused version.
