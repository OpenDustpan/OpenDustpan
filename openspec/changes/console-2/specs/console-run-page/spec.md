## ADDED Requirements

### Requirement: Run timeline from job records

When the console opens a run page, the server SHALL serve the job record with its phases, gates and approvals.

#### Scenario: Open run page for a plan job
- **WHEN** The developer opens the run page for a job ID.
- **THEN** The page shows queued, running and finished phases in order with start and end times.

### Requirement: Live log without secrets

While a job runs, the server SHALL serve masked log chunks in cursor order.

#### Scenario: Follow log while job runs
- **WHEN** The developer watches a running job.
- **THEN** The page appends new masked chunks without full reload and shows the time of the last poll.

#### Scenario: Poll fails then recovers
- **WHEN** A log poll fails and a later poll succeeds.
- **THEN** The page keeps shown chunks, shows the poll error and appends new chunks on the next poll.

### Requirement: Masked plan diff

When the console requests the stored plan, the server SHALL serve the masked plan diff.

#### Scenario: View plan diff
- **WHEN** The developer opens the plan section of a finished plan phase.
- **THEN** The page shows changed resources with secret values masked.

#### Scenario: Plan not yet stored
- **WHEN** The developer opens the plan section before the plan phase stores its plan.
- **THEN** The plan section shows plan pending and retries after the phase ends.

### Requirement: Approval from run page

When an eligible approver submits an approval, the server SHALL record the approval with approver and time.

#### Scenario: Approve waiting apply
- **WHEN** An eligible approver approves from the run page.
- **THEN** The job leaves approval wait and the timeline records the approver and time.

#### Scenario: Resubmit or late submit
- **WHEN** An approver resubmits an approval or submits after the job ends.
- **THEN** The page shows the stored approval or the refusal without duplicate state change.

### Requirement: First render and refresh targets

When the console opens a run page, the server SHALL serve the job, log and plan pages for render within the quality targets.

#### Scenario: Open large plan diff
- **WHEN** The developer opens a run page with a large stored plan while the job runs.
- **THEN** The first page renders within 2 seconds and the live log refreshes within 5 seconds of a poll.
