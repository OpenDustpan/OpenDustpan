## ADDED Requirements

### Requirement: Push starts matching plans

When a push reaches a review request, the server SHALL start a plan job for each matching unit at the head commit of the push.

#### Scenario: Push starts plan at head
- **WHEN** A push reaches a review request that targets the repository and branch of the unit code location.
- **THEN** One plan job exists for each matching unit at the head commit with source kind forge and the namespace service principal as starter.

### Requirement: Intake authentication

When a push delivery fails signature check or repeats a delivery ID, the server SHALL refuse the delivery without a start.

#### Scenario: Spoofed push refused
- **WHEN** A push delivery fails signature check or repeats a delivery ID.
- **THEN** No plan starts and the intake records the refused delivery.

### Requirement: Paths filter over commits

When a push touches no path of the normalized filter, the server SHALL start no plan for that trigger.

#### Scenario: Unrelated push starts nothing
- **WHEN** A push touches no file under the normalized paths filter of the trigger across the union of its commits, counting a rename as a delete plus an add and a force-push on its head files.
- **THEN** No plan job starts for that unit.

### Requirement: Truncated file list

When the forge payload marks the changed-file list as truncated, the server SHALL match every trigger of the same repository and branch and record the truncation on each job.

#### Scenario: Truncation plans with record
- **WHEN** The forge payload marks the changed-file list as truncated.
- **THEN** Plans start for each trigger of the same repository and branch and each job records the truncation.

### Requirement: Default paths normalization

When a trigger is saved with empty paths, the server SHALL store the cleaned working directory of the unit code location as the filter.

#### Scenario: Empty paths defaults to working directory
- **WHEN** A trigger is saved with empty paths.
- **THEN** The stored filter equals the cleaned working directory with a trailing separator so only files under that directory match, a subdirectory location matches only its subtree, and a root location stores the repository root marker that matches every file.

### Requirement: Fork push refused

When a push comes from a fork, the server SHALL refuse the push and write an audit entry.

#### Scenario: Fork push audited
- **WHEN** A push comes from a fork of the target repository or the fork flag is missing.
- **THEN** No plan job starts, no profile opens, and an audit entry records the refused fork push with both repositories.

### Requirement: Fork refusal at start

When the job start sees a missing fork flag, the server SHALL deny the start without a profile open.

#### Scenario: Missing flag refused at start
- **WHEN** The job start sees a missing fork flag even when the intake check is bypassed.
- **THEN** The start is denied with no profile open.

### Requirement: Newer push replaces older plan

When a newer push reaches the same review request, the server SHALL replace the older plan of the same trigger.

#### Scenario: Supersede older plan
- **WHEN** A newer push reaches the same review request before the older plan of the same trigger finishes, including a plan in approval wait or holding a lock.
- **THEN** The queued plan is cancelled and the running plan gets a cancel request, and only the plan at the newer head commit proceeds subject to the open-job limit; a failed cancel fails the older plan without blocking the newer plan, a plan that finished before the cancel lands stays finished, and no restart starts while the cancel is pending.

### Requirement: Concurrent pushes serialized

When two pushes to the same review request arrive at the same time, the server SHALL serialize the supersede steps so only the newer head plan proceeds.

#### Scenario: Concurrent pushes leave newer plan
- **WHEN** Two pushes to the same review request arrive at the same time.
- **THEN** Only the plan at the newer head commit proceeds and the older plan is cancelled.

### Requirement: Authorization at start

When the start names a unit outside the event namespace or an operation other than plan, the server SHALL deny the start.

#### Scenario: Cross-namespace or apply denied
- **WHEN** The start names a unit outside the event namespace or an operation other than plan.
- **THEN** The start is denied and an audit entry records the denial.

### Requirement: Duplicate and terminal pushes

When the same push delivery or the same head commit repeats for one trigger, the server SHALL start no duplicate plan.

#### Scenario: Duplicate delivery ignored
- **WHEN** The same push delivery arrives twice or the head commit already has a plan of the same trigger.
- **THEN** No second plan starts for the same delivery ID and head commit.

### Requirement: Branch delete starts nothing

When a push deletes the branch, the server SHALL start no plan and write an audit entry.

#### Scenario: Delete push ignored
- **WHEN** A push deletes the branch of the review request.
- **THEN** No plan starts and an audit entry records the delete push.

### Requirement: Automatic start record

When a push starts a plan, the server SHALL write an audit entry for the automatic start.

#### Scenario: Start audit written
- **WHEN** A push starts a plan.
- **THEN** An audit entry records the namespace service principal, the trigger, the head commit, and the matched repository and branch.

### Requirement: Starter and output reads

When a push starts a plan, the server SHALL run the start as the namespace service principal and decide output reads by the last saver of the trigger.

#### Scenario: Service principal starts with last-saver reads
- **WHEN** A push starts a plan.
- **THEN** The job carries the namespace service principal as starter, unit profile credentials come from the profile attachment authority, and output reads use the last saver of the trigger.

### Requirement: Trigger detail visibility

When a person reads one review-request-push trigger, the server SHALL show its target repository, target branch, and normalized paths filter.

#### Scenario: Detail shows match fields
- **WHEN** A person reads one review-request-push trigger.
- **THEN** The read shows the target repository, the target branch, and the normalized paths filter.
