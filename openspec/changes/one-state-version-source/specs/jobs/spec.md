## ADDED Requirements

### Requirement: A stale plan never applies
The server SHALL refuse the dispatch of an apply when the current state
version of the unit differs from the version that the plan read. The current
state version SHALL come from the state write of the unit's tool, recorded in
the same transaction as that write.

#### Scenario: State changes after approval (OpenTofu)
- **GIVEN** an approved plan of workspace `payments-prod` that read version A
- **WHEN** a `tofu apply` from another machine writes version B, and the job dispatches its apply
- **THEN** the dispatch fails with `ErrPlanStale`, the reconciler sends the job back to its plan phase (`replan`, as today), and the replan reason names versions A and B

#### Scenario: State changes in the middle of a Pulumi update
- **GIVEN** a plan that read a checkpoint of a running update
- **WHEN** the same update saves another checkpoint with other content
- **THEN** the dispatch fails with `ErrPlanStale`

#### Scenario: Restore after the plan
- **GIVEN** an approved plan that read version A
- **WHEN** a portable restore puts back another state of the unit
- **THEN** the dispatch fails with `ErrPlanStale`

#### Scenario: State unchanged
- **GIVEN** an approved plan that read version A
- **WHEN** no state write happens before the dispatch
- **THEN** the apply runs


### Requirement: A final state version closes write-back jobs and feeds triggers
A final state version SHALL close the awaiting write-back jobs of its unit and
SHALL be the version that `follows` triggers read, in the same transaction as
the state write.

#### Scenario: Lock and unlock without a write
- **GIVEN** workspace `vpc` with final version A
- **WHEN** a `tofu plan` locks and unlocks the state and writes nothing
- **THEN** the final version stays A and no `follows` plan starts

#### Scenario: Allowed unlocked write
- **GIVEN** a workspace that allows writes without a lock
- **WHEN** a write without a lock makes version B current
- **THEN** B is final, and a waiting write-back job of the workspace closes

#### Scenario: Follows trigger fires
- **GIVEN** unit `network` with a `follows` trigger on unit `vpc`
- **WHEN** an apply of `vpc` releases its state lock
- **THEN** a plan of `network` starts
