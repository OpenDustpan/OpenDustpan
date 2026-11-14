## ADDED Requirements

### Requirement: show-running-while-job-runs

When a job for a unit enters the live set, the server SHALL show running for that unit.

#### Scenario: job starts then ends
- **WHEN** A plan, apply or destroy job for the unit starts and then ends
- **THEN** Inventory shows running with the start time until the job ends, then running clears.

#### Scenario: plan-only job shows running
- **WHEN** A plan-only job for the unit runs
- **THEN** Inventory shows running while the plan-only job runs and never shows failed for it.

### Requirement: show-queued-while-job-waits

While a job for a unit waits for dispatch, the server SHALL show queued for that unit.

#### Scenario: job queued then dispatched
- **WHEN** A job for the unit waits for a runner
- **THEN** Inventory shows queued until dispatch, then running replaces queued.

#### Scenario: queued job cancelled before dispatch
- **WHEN** A queued job for the unit is cancelled before dispatch
- **THEN** Inventory shows no queued for that job.

### Requirement: show-awaiting-approval

While an apply job for a unit waits for approvals, the server SHALL show awaiting-approval for that unit.

#### Scenario: apply waits then approved
- **WHEN** An apply job for the unit waits for approvals
- **THEN** Inventory shows awaiting-approval until approval releases the job.

#### Scenario: approval rejected or denied
- **WHEN** An apply job for the unit is rejected, denied, or times out in the approval wait
- **THEN** Inventory shows no awaiting-approval for that job.

### Requirement: set-failed-on-failed-change

When the latest ended change of a unit failed, the server SHALL show failed for that unit.

#### Scenario: failed apply sets failed
- **WHEN** An apply or destroy job for the unit ends as failed
- **THEN** Inventory shows failed with the server end time of that change.

#### Scenario: failed Pulumi update sets failed
- **WHEN** A Pulumi update of kind update, destroy or import for the unit ends as failed
- **THEN** Inventory shows failed with the server end time of that update.

#### Scenario: preview does not set failed
- **WHEN** A preview, refresh, plan-only job or drift check for the unit ends as failed
- **THEN** Inventory shows no new failed for that run.

### Requirement: clear-failed-on-later-change

When a later change identity of a unit ends with success, the server SHALL clear failed for that unit.

#### Scenario: success after failure clears
- **WHEN** A later change identity of the unit ends with success
- **THEN** Inventory shows no failed for the unit.

#### Scenario: job end first then update end
- **WHEN** A Pulumi job end arrives first and its update end for the same change identity arrives second
- **THEN** Inventory shows the same failed value after each arrival.

#### Scenario: update end first then job end
- **WHEN** A Pulumi update end arrives first and its job end for the same change identity arrives second
- **THEN** Inventory shows the same failed value after each arrival.

#### Scenario: single job end sets failed
- **WHEN** A failed apply job end arrives and no update end for that change ever arrives
- **THEN** Inventory shows failed with the server end time of the job end.

### Requirement: keep-failed-on-same-change-disagreement

If the two ends of one change identity disagree, then the server SHALL keep failed for that unit.

#### Scenario: ends disagree keeps failed
- **WHEN** The job end and the update end of one change identity disagree on success
- **THEN** Inventory keeps failed until a later change identity with success clears it.

### Requirement: order-ends-by-server-time

Where two change ends compete, the server SHALL order them by server end time and break ties by greater change identity.

#### Scenario: late older end does not overwrite
- **WHEN** A failed end of an older change identity arrives after a newer change identity ended
- **THEN** Inventory keeps the failed value of the newer server end time.

#### Scenario: equal end times break by identity
- **WHEN** Two change identities of one unit carry the same server end time
- **THEN** Inventory shows the value of the greater change identity.

### Requirement: clear-live-on-terminal

When a job for a unit ends as cancelled, timed out, lost-claim or superseded, the server SHALL clear its live states.

#### Scenario: cancelled clears live states
- **WHEN** A job for the unit ends as cancelled, timed out, lost-claim or superseded
- **THEN** Inventory shows no running, queued or awaiting-approval for that job.

### Requirement: reconcile-live-states

While the server starts or takes over a namespace, the server SHALL reconcile stored live states against live jobs.

#### Scenario: restart clears orphan live states
- **WHEN** The server restarts or the namespace fails over while live-state rows exist
- **THEN** Inventory shows no running, queued or awaiting-approval for jobs that are terminal or unknown.

#### Scenario: live job keeps running
- **WHEN** A runner still executes a job whose record is still live after a restart
- **THEN** Inventory keeps running for that job.

### Requirement: fence-failure-rolls-back

If the fence check fails in the transaction of a job change, then the server SHALL roll back that transaction.

#### Scenario: fence check fails
- **WHEN** The fence check fails in the transaction of a job change after a failover
- **THEN** The store keeps neither the job change nor the state write.

### Requirement: set-apply-needed-for-stored-plan

When a ready stored plan with unapplied changes exists for a unit, the server SHALL show apply-needed for that unit.

#### Scenario: plan with changes sets flag
- **WHEN** A ready stored plan with changes for the unit exists and no later apply consumed it
- **THEN** Inventory shows apply-needed for the unit.

### Requirement: clear-apply-needed-on-consume

When no ready stored plan with changes remains for a unit, the server SHALL clear apply-needed for that unit.

#### Scenario: apply clears flag
- **WHEN** No ready stored plan with changes remains because the plan was applied, superseded, expired or invalid
- **THEN** Inventory shows no apply-needed for the unit.

#### Scenario: newer plan keeps flag
- **WHEN** A newer ready plan supersedes an older plan and the newer plan still has changes
- **THEN** Inventory keeps apply-needed for the newer plan.

### Requirement: clear-states-on-unit-delete

When a unit is deleted, the server SHALL delete its stored state rows in the same transaction.

#### Scenario: delete clears rows
- **WHEN** A unit is deleted and later recreated with a new stable ID
- **THEN** No state rows remain for that unit ID and the recreated unit shows no states.

#### Scenario: rename keeps rows
- **WHEN** A unit is renamed without changing its stable ID
- **THEN** Inventory keeps the states of the unit under its stable ID.

### Requirement: stable-wire-values

Where the server writes the five states, the server SHALL use the stable wire values running, queued, awaiting-approval, apply-needed and failed.

#### Scenario: wire values table
- **WHEN** The wire-value test runs for the five states
- **THEN** The table holds running, queued, awaiting-approval, apply-needed and failed in kebab case.

### Requirement: read-states-auth

Where the inventory shows states, the server SHALL require unit:read on each unit.

#### Scenario: caller without read sees no states
- **WHEN** A caller without unit:read on the unit reads the unit list or the unit
- **THEN** The server denies the read with no states.
