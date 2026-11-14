## ADDED Requirements

### Requirement: Pass a gate in error once

When a write evaluation ends in error and the request carries valid break-glass, the server SHALL permit the write once for the same holder, unit stable ID and exact operation kind within 15 minutes.

#### Scenario: Error pass
- **WHEN** a write evaluation errors and the request carries the grant, a reason of 1 to 500 characters and fresh authentication
- **THEN** the write proceeds, one audit entry of kind override is written and the record keeps result error with a break-glass marker

#### Scenario: Replay
- **WHEN** the holder uses the same pass a second time
- **THEN** the write is refused

#### Scenario: Other unit or kind
- **WHEN** the holder uses the pass on another unit or operation kind
- **THEN** the write is refused and no pass is consumed

### Requirement: Refuse bad break-glass

If the reason is empty, longer than 500 characters or holds control characters, or fresh authentication is stale, or the caller lacks the action, then the server SHALL refuse the write without a pass.

#### Scenario: Bad reason
- **WHEN** the reason is empty or too long
- **THEN** the server answers 422 and creates no pass

#### Scenario: No grant
- **WHEN** the caller lacks namespace:break-gate-error
- **THEN** the server answers 403 and creates no pass

### Requirement: Deny still refuses

If a write evaluation holds a deny, then the server SHALL refuse the write when break-glass is set.

#### Scenario: Deny with break-glass
- **WHEN** the evaluation has a deny and the request sets break-glass
- **THEN** the write is refused and no pass is consumed

### Requirement: Atomic single use

When the server consumes a pass, the server SHALL consume it with a conditional update in the same transaction as the write.

#### Scenario: Concurrent writes
- **WHEN** two writes present the same unused pass
- **THEN** one write proceeds and the other is refused

### Requirement: Audit failure fails the write

If the override audit entry cannot be written, then the server SHALL fail the write without consuming the pass.

#### Scenario: Audit fails
- **WHEN** the audit entry cannot be written
- **THEN** the write fails and the pass stays unused
