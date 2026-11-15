## ADDED Requirements

### Requirement: Only a person approves turning the audit chain off

When the server receives an approval of the change of the audit chain to off, it SHALL refuse it unless the caller is a stored principal of kind person, and SHALL write a denied audit entry in the server scope for each refusal.

#### Scenario: A workload approves
- **WHEN** a workload principal approves the change to off
- **THEN** the server answers 403, starts no migration, and writes one denied entry in the server scope

#### Scenario: A subject with no principal row approves
- **WHEN** a subject with no principal row reaches the check of the approval
- **THEN** the check refuses it, and the server writes one denied entry in the server scope

#### Scenario: The principal reader is missing
- **WHEN** the server has no principal reader and an approval of the change to off arrives
- **THEN** the server refuses the approval
