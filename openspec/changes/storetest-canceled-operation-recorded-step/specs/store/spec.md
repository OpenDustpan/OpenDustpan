## ADDED Requirements

### Requirement: Canceled RecordStep precedence

When a live operation has cancel requested and one step recorded, the store SHALL return ErrCanceled from RecordStep.

#### Scenario: Canceled operation with recorded step on retry
- **WHEN** a live operation has cancel requested and one step already recorded
- **THEN** RecordStep returns ErrCanceled.

#### Scenario: Canceled operation with differing fence step
- **WHEN** a live canceled operation has a recorded step and the fence names a different step
- **THEN** RecordStep returns ErrCanceled and not a step error.

### Requirement: Canceled peek precedence

When a live operation has cancel requested and one step recorded, the store SHALL return ErrCanceled from the restore peek check.

#### Scenario: Canceled operation with recorded step on peek
- **WHEN** a live operation has cancel requested and one step already recorded
- **THEN** the restore peek check returns ErrCanceled.

#### Scenario: Canceled operation with differing fence step on peek
- **WHEN** a live canceled operation has a recorded step and the fence names a different step
- **THEN** the restore peek check returns ErrCanceled and not a step error.
