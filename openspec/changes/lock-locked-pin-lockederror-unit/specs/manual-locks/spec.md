## ADDED Requirements

### Requirement: refused-unit-is-pinned

When the server refuses a write under a manual lock, the server SHALL name the refused unit tool, namespace and name in the refusal.

#### Scenario: workspace-create-under-namespace-lock
- **WHEN** workspace create is refused under a namespace lock
- **THEN** the refusal names the refused workspace tool, namespace and name

#### Scenario: workspace-restore-under-namespace-lock
- **WHEN** workspace restore is refused under a namespace lock
- **THEN** the refusal names the refused workspace tool, namespace and name

#### Scenario: stack-restore-under-namespace-lock
- **WHEN** stack restore is refused under a namespace lock
- **THEN** the refusal names the refused stack tool, namespace and name

#### Scenario: sqlite-refuse-restore-under-lock
- **WHEN** the SQLite store refuses a restore under a lock
- **THEN** the refusal names the refused unit tool, namespace and name

#### Scenario: postgres-refuse-restore-under-lock
- **WHEN** the PostgreSQL store refuses a restore under a lock
- **THEN** the refusal names the refused unit tool, namespace and name

#### Scenario: trigger-fire-manual-lock
- **WHEN** the trigger fire route is refused by a manual lock
- **THEN** the refusal names the refused unit tool, namespace and name

### Requirement: locked-and-refused-objects-differ

When the server answers a manual-lock refusal, the server SHALL report the locked object separately from the refused unit.

#### Scenario: swap-is-visible
- **WHEN** the locked and refused objects are swapped in the Pulumi and trigger answer tests
- **THEN** the answer test fails
