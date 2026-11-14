## ADDED Requirements

### Requirement: State facts from both states

When a tool-neutral operation holds the old and the new state, the server SHALL fill state.resources from the new state and state.removed from the removed resources.

#### Scenario: Tool-neutral update with removals
- **WHEN** A tool-neutral operation holds the old and the new state
- **THEN** The fact document holds state.available true, state.resources from the new state and state.removed from the removed resources.

### Requirement: Client-encrypted body continues

When the state body is missing or client-encrypted, the server SHALL continue the write with state.available false and the reason recorded.

#### Scenario: Encrypted state body
- **WHEN** The state body is missing or client-encrypted
- **THEN** The fact document holds state.available false with the reason and the write continues under the gates that apply.

### Requirement: Allow record linkage

When a state.write evaluation allows the operation, the server SHALL write the allow record in the same transaction as the step.

#### Scenario: Allowed write
- **WHEN** A state.write evaluation allows the operation
- **THEN** The step commits with its decision record and the audit entry of the step names the record ID.

### Requirement: Refusal record own transaction

When a state.write evaluation refuses or waits, the server SHALL write the record in its own transaction.

#### Scenario: Denied write
- **WHEN** A state.write evaluation denies the operation
- **THEN** The refusal record commits in its own transaction and the error names the record ID.

#### Scenario: Approval wait
- **WHEN** A state.write evaluation waits for approvals
- **THEN** The wait record commits in its own transaction and names the pending requirement.

#### Scenario: Refusal names attempt only
- **WHEN** A refused write is followed by no committed step
- **THEN** The record states a refused attempt and never states that a step committed.

### Requirement: Rollback record truth

When the step does not commit, the server SHALL roll back the allow record with it.

#### Scenario: Step fails after guard
- **WHEN** The store loses the fence or returns an error before commit
- **THEN** No record claims the step committed.

#### Scenario: Concurrent write loses fence
- **WHEN** A concurrent write changes the stored state between fact build and commit
- **THEN** Both the step and the allow record roll back.

### Requirement: Fixed controls in record

When the server evaluates state.write, the server SHALL list the workspace lock, manual lock, freeze, override switch and safe delete states in the decision record.

#### Scenario: Locked workspace write
- **WHEN** A state.write evaluation runs on a locked workspace
- **THEN** The record lists the workspace lock state and the manual lock, freeze, override switch and safe delete states.

### Requirement: Approval defaults

When an approval default applies at state.write, the server SHALL enforce its requirement with distinct eligible approvers before the write proceeds.

#### Scenario: Default needs two approvers
- **WHEN** An approval default at state.write needs two approvers
- **THEN** The result waits for two distinct eligible signatures before the write proceeds.

#### Scenario: Author signature refused
- **WHEN** The change author signs at state.write
- **THEN** The author signature does not count toward the requirement.

#### Scenario: Repeated signer refused
- **WHEN** One person signs twice at state.write
- **THEN** The second signature of the same person does not count toward the requirement.

#### Scenario: Ineligible signer refused
- **WHEN** A signer outside the eligible approvers signs at state.write
- **THEN** The signature does not count toward the requirement.

#### Scenario: Fence lost during wait
- **WHEN** The store loses the fence while a state.write approval wait is pending
- **THEN** The wait record stays and the write does not proceed until the fence holds and the requirement is met.

### Requirement: Limits at state.write routes

When a state.write route asks for an action on a unit, namespace, collection or installation object, the server SHALL evaluate the access limits through the limited authorizer before grants alone allow the call.

#### Scenario: Limit on state.write unit call
- **WHEN** An access limit refuses a state.write call on a unit object
- **THEN** The call returns 403 gate.limited with the record ID.

#### Scenario: Limit on state.write namespace call
- **WHEN** An access limit refuses a state.write call on a namespace, collection or installation object
- **THEN** The call returns 403 gate.limited with the record ID.

### Requirement: Simulate replay

When a caller replays stored state.write records through the simulate route, the server SHALL evaluate the draft rules against the stored fact documents.

#### Scenario: Replay stored state.write records
- **WHEN** A caller replays stored state.write records through the simulate route
- **THEN** The answer gives the stored result and the new result for each readable record.
