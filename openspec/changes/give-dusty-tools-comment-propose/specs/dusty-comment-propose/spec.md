## ADDED Requirements

### Requirement: Comment on job

When Dusty calls the NEW tool comment_job on a non-terminal job with no change-order origin, the server SHALL store a comment of at most 4000 bytes.

#### Scenario: Comment stored
- **WHEN** Dusty calls comment_job on a running job
- **THEN** the job holds the comment and one agent-tool entry exists.

#### Scenario: Change-order job refused
- **WHEN** Dusty calls comment_job on a job with a change-order origin
- **THEN** the call fails with job.ErrInvalid and no comment is stored.

### Requirement: Propose stays draft

When Dusty calls the NEW tool propose_change_order, the server SHALL create a draft change order that stays a draft until its person confirms it.

#### Scenario: Draft made
- **WHEN** Dusty calls propose_change_order with a valid plan
- **THEN** a draft exists with the person as proposer and the confirmation step pending.

#### Scenario: Agent confirm refused
- **WHEN** an agent principal confirms the draft
- **THEN** the confirm fails and one denied entry exists.

### Requirement: No approve

If an agent principal sends an approve or reject verdict, then the server SHALL refuse it.

#### Scenario: Verdict refused
- **WHEN** an agent principal approves a change order
- **THEN** the verdict fails with ErrAgentDenied.
