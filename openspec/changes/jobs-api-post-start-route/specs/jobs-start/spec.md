## ADDED Requirements

### Requirement: Declare start union

While the jobs POST start operation exists, the server SHALL declare the union {unit:run-plan, unit:run-apply} on that operation.

#### Scenario: Union declaration
- **WHEN** a caller reads the declaration of the jobs POST start operation
- **THEN** the API description and the route test show the union {unit:run-plan, unit:run-apply} on the jobs POST start operation.

### Requirement: State per-kind meaning

While the jobs POST start operation exists, the server SHALL state the per-kind mapping in its operation text.

#### Scenario: Per-kind text
- **WHEN** a caller reads the jobs POST start operation text
- **THEN** the operation text states that a plan start checks unit:run-plan, an apply start checks unit:run-apply, and a caller needs only the action of its own start.

### Requirement: Guard declaration drift

While the jobs POST start operation exists, the server SHALL keep the NEW TestStartActionsMatchDeclaration test looping over every ExecutorKind value.

#### Scenario: Drift test
- **WHEN** the declared union differs from the set that startActionFor returns for any ExecutorKind value
- **THEN** the NEW TestStartActionsMatchDeclaration test fails.

#### Scenario: Apply refused without apply grant
- **WHEN** a caller with only unit:run-plan starts an apply
- **THEN** the server refuses the apply start with 403 missing unit:run-apply and the refusal names the read of effective grants.
