## ADDED Requirements

### Requirement: startFromMerge from merge event

When the server receives a merged pull request event, the server SHALL start 1 apply job per touched unit from the default branch at the merge commit.

#### Scenario: startFromMerge creates apply at merge commit
- **WHEN** the forge reports a merged pull request
- **THEN** 1 apply job exists per touched unit, pinned to the default branch and the merge commit sha

#### Scenario: Branch push does not apply
- **WHEN** a push reaches the pull request branch
- **THEN** no apply job starts

#### Scenario: Later push does not move pinned job
- **WHEN** a new push reaches the default branch after the merge
- **THEN** the existing job keeps its merge commit sha and a new merge creates a new job

#### Scenario: Non-default branch merge starts nothing
- **WHEN** a merged pull request event names a branch that is not the default branch
- **THEN** no apply job starts and the result names the non-default branch

### Requirement: startFromMerge replan at merge commit

When a merge apply job starts, the server SHALL replan at the pinned merge commit before the apply phase.

#### Scenario: Changed replan resets approvals
- **WHEN** the replan at the merge commit differs from the reviewed plan
- **THEN** the job resets approvals per the reused approval rules before the apply phase

#### Scenario: Identical replan keeps signatures
- **WHEN** the replan at the merge commit is identical to the reviewed plan
- **THEN** the job keeps existing signatures and continues to gates

### Requirement: Keep gates on merge apply

When the merge apply reaches a decision point, the server SHALL evaluate gates and approvals before the apply phase.

#### Scenario: Gate refuses after merge
- **WHEN** a gate in enforce mode refuses the merge commit after it passed on the branch
- **THEN** the job ends as failed, keeps its existing signatures unchanged, and names the new decision record

### Requirement: Approval wait

While a merge apply waits for an approval, the server SHALL keep the job in awaiting-approval until each requirement gains its signatures.

#### Scenario: Apply waits for approval
- **WHEN** an approval requirement lacks a signature at the merge apply
- **THEN** the job stays awaiting-approval and names the missing approval

### Requirement: Pinned commit visible

The server SHALL expose the pinned default branch name and the merge commit sha on the job read route for each merge apply job.

#### Scenario: Job shows pinned commit
- **WHEN** a merge apply job exists
- **THEN** the job read returns the default branch name and the merge commit sha for the job

### Requirement: Duplicate merge creates one job

When a duplicate merged pull request event arrives, the server SHALL create at most 1 apply job per touched unit for that merge event.

#### Scenario: Replayed merge creates no second job
- **WHEN** the same merged pull request event arrives twice
- **THEN** each touched unit has at most 1 job per unit for that merge event

#### Scenario: Merge with no touched units
- **WHEN** a merged pull request touches no unit
- **THEN** no apply job starts

#### Scenario: Partial write retries only missing
- **WHEN** job creation for a merge event fails midway for some units
- **THEN** the retry creates only the missing unit jobs and creates no second job for a unit that already has 1 job for that merge event

### Requirement: Survive restart

When the server restarts after a merge event and before job creation completes, the server SHALL still create exactly 1 apply job per touched unit for that event.

#### Scenario: Restart creates exactly one job
- **WHEN** the server restarts after the merge event and before job creation completes
- **THEN** each touched unit has exactly 1 apply job for the merge event after restart

### Requirement: Missing sha refused

If a merged pull request event holds no usable merge commit sha, then the server SHALL refuse job creation with an error that names the missing sha.

#### Scenario: Merge without sha starts nothing
- **WHEN** a merged pull request event holds no usable merge commit sha
- **THEN** no apply job starts and the result names the missing merge commit sha

### Requirement: Concurrent merges queue in merge order

When a merged pull request names a unit with an apply already running, the server SHALL create the new merge apply job as queued behind the running apply.

#### Scenario: Second merge waits
- **WHEN** a merged pull request names a unit with an apply already running from an earlier merge
- **THEN** the new job stays queued until the running apply ends and runs after it in merge time order

#### Scenario: Failed earlier apply does not cancel queued
- **WHEN** the running apply fails while a newer merge job waits queued
- **THEN** the queued merge job still runs after the earlier job ends as failed

### Requirement: Missing commit fails checkout

If the forge no longer serves the pinned merge commit sha at checkout, then the server SHALL fail the merge apply job with an error that names the missing sha.

#### Scenario: Pinned commit gone at checkout
- **WHEN** the forge no longer serves the pinned merge commit sha at checkout
- **THEN** the job ends as failed and names the missing commit sha
