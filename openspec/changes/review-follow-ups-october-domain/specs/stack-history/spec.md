## ADDED Requirements

### Requirement: Map deleted stack to not found

When the stack is deleted between the existence check and ListUpdates, the server SHALL return ErrNotFound from History.

#### Scenario: Deleted between reads
- **WHEN** the stack is deleted between the existence check and ListUpdates
- **THEN** History returns ErrNotFound with errors.Is match

#### Scenario: Existing stack without updates
- **WHEN** the stack exists and has no updates
- **THEN** History returns an empty list

### Requirement: Pass other history store errors through

When ListUpdates fails for a reason other than a missing stack, the server SHALL return that store error from History unchanged.

#### Scenario: Other store failure
- **WHEN** ListUpdates fails for a reason other than a missing stack
- **THEN** History returns the store error unchanged and not as ErrNotFound
