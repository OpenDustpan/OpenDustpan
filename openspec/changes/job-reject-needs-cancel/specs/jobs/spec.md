## ADDED Requirements

### Requirement: A rejection by another person needs the cancel action
The server SHALL accept a verdict `reject` on an approval gate only from a principal that holds `job:approve` on the unit of the job, and, when the principal is not `Job.StartedBy`, also `job:cancel` on that unit. The server SHALL check the second action in the job service and in the HTTP handler. When the authorizer fails, the server SHALL deny. A denial SHALL write one audit entry that names `job:cancel` and the unit, and SHALL NOT change the job.

#### Scenario: Another person without the cancel action
- **GIVEN** a job that a person started, and a second person with `job:approve` and without `job:cancel` on its unit
- **WHEN** the second person rejects the job
- **THEN** the answer is 403 `access.denied` and names `job:cancel`
- **AND** the job keeps its state and one audit entry records the refusal

#### Scenario: Another person with both actions
- **GIVEN** a second person with `job:approve` and `job:cancel` on the unit
- **WHEN** the second person rejects the job
- **THEN** the job ends in the state `rejected` and the audit entry names `job:approve`

#### Scenario: The requester without the cancel action
- **GIVEN** a requester with `job:approve` and without `job:cancel`, as a custom grant gives
- **WHEN** the requester rejects its own job
- **THEN** the job ends in the state `rejected`

#### Scenario: The authorizer fails
- **WHEN** the authorizer returns an error for `job:cancel` during a rejection by another person
- **THEN** the answer is 403 `access.denied` and the job keeps its state

#### Scenario: An approval is not affected
- **WHEN** a person with `job:approve` and without `job:cancel` approves a job that another person started
- **THEN** the approval follows the rules of the approval gate and needs no `job:cancel`

#### Scenario: The call that the docs page shows works against a real server
- **GIVEN** a running server, a job that a person started, and a second person with `job:approve` and without `job:cancel` on its unit
- **WHEN** the e2e suite lists the grants of the unit and the second person rejects the job
- **THEN** the grant list shows `job:approve` and not `job:cancel` for the second person
- **AND** the rejection answers 403 `access.denied` and names `job:cancel`, as the docs page `docs/guides/approval-rules.md` shows
