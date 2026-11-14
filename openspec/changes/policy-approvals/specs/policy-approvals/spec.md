## ADDED Requirements

### Requirement: Destroy needs fresh step

When an approval covers a change that holds a destroy, the server SHALL refuse the approval without a fresh authentication in the window.

#### Scenario: Destroy approval without fresh step
- **WHEN** an approval covers a change that holds a destroy and carries no fresh authentication in the window
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes

#### Scenario: No enrolled factor
- **WHEN** a person with no enrolled factor approves a change that holds a destroy
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes, and the refusal text names the enrollment page and each way to enroll the first factor

#### Scenario: Destroy approval with access token
- **WHEN** a destroy approval uses an access token
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes

### Requirement: Composed step-6 run

When a runner plan job and apply job run the milestone step-6 path, the server SHALL wait in awaiting-approval until the owning approval, limit and gate checks all allow the apply.

#### Scenario: Composed wait and start
- **WHEN** a runner plan job and apply job run the step-6 path for a stack and a workspace
- **THEN** the apply waits in awaiting-approval after one approval and starts after the owning checks allow it

#### Scenario: Composed refusals surface
- **WHEN** a limit, an enforce gate, or a new or stale plan meets the composed run
- **THEN** the run surfaces the owning 403 limit refusal with its decision record, the owning enforce gate refusal, and the owning 409 stale or digest refusal
