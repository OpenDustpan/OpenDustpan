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
- **THEN** The page appends new masked chunks without full reload.

### Requirement: Masking across chunk boundary

When a secret value spans two log chunks, the server SHALL mask the joined value across the boundary.

#### Scenario: Secret split across two chunks
- **WHEN** A runner prints a secret value split across two log chunks.
- **THEN** Both chunks arrive masked and the joined text holds no secret value.

### Requirement: Masked plan diff

When the console requests the stored plan, the server SHALL serve the masked plan diff.

#### Scenario: View plan diff
- **WHEN** The developer opens the plan section of a finished plan phase.
- **THEN** The page shows changed resources with secret values masked.

### Requirement: Approval from run page

When an eligible approver submits an approval, the server SHALL record the approval with approver and time.

#### Scenario: Approve waiting apply
- **WHEN** An eligible approver approves from the run page.
- **THEN** The job leaves approval wait and the timeline records the approver and time.

### Requirement: Approval refusal stays in wait

If a principal without the approve action submits an approval, then the server SHALL refuse the approval with 403.

#### Scenario: Approve without approve action
- **WHEN** A principal without the approve action on the unit submits an approval.
- **THEN** The server answers 403 and the job stays in approval wait.

### Requirement: Unknown or foreign job not found

When the console requests an unknown job ID or a job ID from another namespace, the server SHALL answer 404.

#### Scenario: Open unknown job ID
- **WHEN** The console requests a job ID that is unknown or belongs to another namespace.
- **THEN** The server answers 404 and the page links back to the unit list.

### Requirement: Read refused without read action

If a principal without the read action requests a job, log or plan, then the server SHALL refuse the read with 403.

#### Scenario: Open job without read action
- **WHEN** A principal without the read action on the unit requests the job, log or plan.
- **THEN** The server answers 403 and the page shows no job, log or diff.

### Requirement: Log resume after close

When the log stream closes, the server SHALL serve further chunks from the supplied cursor after retry.

#### Scenario: Runner stops mid-log
- **WHEN** The log stream closes before the job ends.
- **THEN** The page offers retry and resumes from the last cursor without duplicate chunks.

### Requirement: Persisted log cursor

When the server restarts, the server SHALL keep stored log chunks readable from each issued cursor.

#### Scenario: Restart server mid-log
- **WHEN** The server restarts before the developer retries the log.
- **THEN** Retry from the last cursor returns the next chunks with no loss and no duplicates.

### Requirement: Stale log and diff cursor

If the console supplies a cursor past the retained range or past the total count, then the server SHALL answer 410.

#### Scenario: Retry with expired cursor
- **WHEN** The console retries with a cursor past the retained range or past the total count.
- **THEN** The server answers 410 and the page reloads from the first cursor.

### Requirement: Duplicate approval

When a duplicate approval arrives, the server SHALL return the stored approval without state change.

#### Scenario: Approve twice
- **WHEN** An approver resubmits the same approval.
- **THEN** The second submit returns the stored approval and the job state does not change.

### Requirement: Late approval after cancel

If an approval arrives after cancel with no stored approval, then the server SHALL refuse the approval with 409.

#### Scenario: Approve after cancel with no prior approval
- **WHEN** A principal submits a first approval after the job was cancelled.
- **THEN** The server answers 409 and records no approval.

### Requirement: Second distinct approver

When a second eligible approver submits an approval for the same wait, the server SHALL record a second signature without replacement.

#### Scenario: Two approvers for one wait
- **WHEN** Two different eligible approvers approve the same waiting apply.
- **THEN** The job holds two signatures and the timeline names both approvers.

### Requirement: Paginated plan diff

When the stored plan exceeds the page limit, the server SHALL serve the masked diff in pages with a next cursor.

#### Scenario: Open large plan diff
- **WHEN** The developer opens a stored plan that exceeds the page limit.
- **THEN** The page shows the first page with a next cursor and total count.

### Requirement: Polling for live records

When the console polls a running job, the server SHALL serve the current job and log records on each poll.

#### Scenario: Watch running job on interval
- **WHEN** The developer watches a running job with 5 second polls and manual refresh.
- **THEN** The page shows new timeline entries and states the time of the last poll.
