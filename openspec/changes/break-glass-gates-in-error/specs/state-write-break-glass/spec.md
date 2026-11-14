## ADDED Requirements

### Requirement: Pass a gate in error once

When a state.write evaluation ends in error and the request carries break-glass, the server SHALL permit the write once for the same holder, unit stable ID, and exact operation kind within 15 minutes.

#### Scenario: Error pass with reason and fresh auth
- **WHEN** A state.write evaluation ends in error and the request carries break-glass with the grant, a reason of 1 to 500 characters, and fresh authentication
- **THEN** The write proceeds, the server writes one audit entry of kind override, and the decision record keeps result error with a break-glass marker.

#### Scenario: Replay of a used pass
- **WHEN** The same holder retries the same unit and operation kind with the same pass a second time
- **THEN** The second write is refused with gate.ErrRuleFailed and no new write occurs.

#### Scenario: Expired pass
- **WHEN** The holder retries more than 15 minutes after the pass was granted on the store clock
- **THEN** The write is refused with gate.ErrRuleFailed.

#### Scenario: Pass for another unit or kind
- **WHEN** The holder uses a pass granted for one unit stable ID and operation kind on another unit or another operation kind
- **THEN** The write is refused with gate.ErrRuleFailed and no pass is consumed for the other unit or kind.

#### Scenario: Unlocked-write kind needs its own pass
- **WHEN** The holder uses a pass granted for operation kind state-push on an unlocked-write
- **THEN** The write is refused with gate.ErrRuleFailed.

#### Scenario: Unit-import kind needs its own pass
- **WHEN** The holder uses a pass granted for another operation kind on a unit-import
- **THEN** The import unit is skipped and no pass is consumed.

### Requirement: Refuse bad break-glass

If the break-glass reason is empty or longer than 500 characters or holds control characters, or fresh authentication is stale, or the caller lacks namespace:break-gate-error, then the server SHALL refuse the write without creating or consuming a pass.

#### Scenario: Empty or overlong reason
- **WHEN** The break-glass reason is empty or longer than 500 characters
- **THEN** The server refuses the write with 422 and creates no pass row.

#### Scenario: Control characters in reason
- **WHEN** The break-glass reason holds control characters or invalid UTF-8
- **THEN** The server refuses the write with 422 and creates no pass row.

#### Scenario: Stale authentication or missing grant
- **WHEN** Fresh authentication is stale or the caller lacks namespace:break-gate-error
- **THEN** The server refuses the write with 403 and creates no pass row.

### Requirement: Atomic single use

When the server consumes a break-glass pass, the server SHALL consume it with a conditional update in the same transaction as the state write so only one concurrent caller wins.

#### Scenario: Concurrent writes with one pass
- **WHEN** Two writes present the same unused pass at the same time
- **THEN** Only one write proceeds and the other is refused with gate.ErrRuleFailed.

#### Scenario: Crash before commit
- **WHEN** The server restarts between the consume and the commit
- **THEN** The write is not visible and the pass stays unused.

### Requirement: Audit failure fails the write

If the override audit entry cannot be written, then the server SHALL fail the state write without consuming the pass.

#### Scenario: Override audit write fails
- **WHEN** The override audit entry cannot be written
- **THEN** The state write fails and the pass stays unused.

### Requirement: Mark the passed write

When the server permits a write by break-glass, the server SHALL write one audit entry of kind override with the principal, reason, and gate IDs.

#### Scenario: Passed write audit and record
- **WHEN** The server permits a write by break-glass
- **THEN** One audit entry of kind override holds the principal, reason, and gate IDs, and the decision record names the break-glass use.

### Requirement: Deny still refuses

If a state.write evaluation ends in deny, then the server SHALL refuse the write even when break-glass is set.

#### Scenario: Deny with break-glass
- **WHEN** A state.write evaluation ends in deny and the request sets break-glass
- **THEN** The write is refused and no pass is consumed.

### Requirement: Error plus deny refuses

If a state.write evaluation holds both an error and a deny, then the server SHALL refuse the write even when break-glass is set.

#### Scenario: Error combined with deny
- **WHEN** A state.write evaluation holds both an error result and a deny outcome and the request sets break-glass
- **THEN** The write is refused and no pass is consumed.

### Requirement: Evaluate mode needs no pass

If a gate in evaluate mode errors at state.write, then the server SHALL proceed with no break-glass and consume no pass.

#### Scenario: Evaluate-mode error proceeds alone
- **WHEN** A gate in evaluate mode errors at state.write
- **THEN** The write proceeds with no break-glass and no pass is created or consumed.
