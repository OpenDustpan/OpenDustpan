## ADDED Requirements

### Requirement: Destroy needs fresh step

When an approval covers a change that holds a destroy, the server SHALL refuse the approval without a fresh authentication in the window that holds for it.

#### Scenario: Destroy approval without fresh step
- **WHEN** an approval covers a change that holds a destroy and carries no fresh authentication in the window
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes

#### Scenario: Stale fresh proof
- **WHEN** the fresh proof expired between approve and dispatch
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes

#### Scenario: Zero window
- **WHEN** freshAuthSeconds is 0
- **THEN** approval is refused without a fresh step at that approval

#### Scenario: Narrower window
- **WHEN** a namespace or rule sets a shorter freshAuthSeconds
- **THEN** the shortest window of installation, namespace and rule decides the fresh step

#### Scenario: No enrolled factor
- **WHEN** a person with no enrolled factor approves a change that holds a destroy
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes, and the refusal text names the enrollment page and each way to enroll the first factor

#### Scenario: Token and agent callers
- **WHEN** a destroy approval uses an access token, a job token, a trust token or an agent credential
- **THEN** approval is refused with 403 mfa.fresh-step-required and nothing changes

#### Scenario: Mixed apply and destroy
- **WHEN** a revision holds both apply and destroy units
- **THEN** the whole approval needs the fresh step

#### Scenario: Break-glass destroy
- **WHEN** a break-glass session approves a change that holds a destroy
- **THEN** approval is refused without a fresh authentication in the window

### Requirement: Wait and reset

While an apply waits in awaiting-approval, the server SHALL start the apply only after the owning approval, limit and gate checks all allow it.

#### Scenario: One approval waits
- **WHEN** only one approval exists for a two-person rule
- **THEN** the apply stays in awaiting-approval and does not start

#### Scenario: New plan voids
- **WHEN** a new plan revision attaches while the apply waits
- **THEN** old signatures stay void with cause new-plan and approvers approve the new digest

#### Scenario: Recheck adds
- **WHEN** the recheck at change.apply adds a requirement after approval
- **THEN** the change returns to awaiting-approval with the new requirement

### Requirement: Composed refusals

When a runner plan job and apply job run the milestone step-6 path, the server SHALL surface each owning 403 limit, enforce gate and 409 stale or digest refusal without change.

#### Scenario: Composed refusals surface
- **WHEN** a limit, an enforce gate, or a new or stale plan meets the composed runner run
- **THEN** the run surfaces the owning 403 limit refusal with its decision record, the owning enforce gate refusal, and the owning 409 stale or digest refusal
