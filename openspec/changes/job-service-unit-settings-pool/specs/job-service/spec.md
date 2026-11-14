## ADDED Requirements

### Requirement: Resolve start inputs from settings

When a job starts, the server SHALL resolve pool, code location, profile reference and steps from the request override else the unit settings else the namespace unit defaults.

#### Scenario: Trigger start with stored location
- **WHEN** A schedule trigger fires with no location in the fire body and the unit settings hold a location.
- **THEN** The server starts a plan job with the pool, code location and profile reference from the unit settings and the stored OpenTofu steps.

#### Scenario: Missing location in request and settings
- **WHEN** A start has no location in the request and none in the unit settings.
- **THEN** The server answers 422, creates no job, and names the unit settings field.

#### Scenario: Pulumi with no stored steps
- **WHEN** A Pulumi start has no steps in the request and none in the unit settings.
- **THEN** The server refuses the start and names the docs command for steps.

#### Scenario: Fire after settings removal
- **WHEN** A trigger fires after the stored code location of its unit was removed.
- **THEN** The server answers 422 and creates no job.

#### Scenario: Override needs extra action
- **WHEN** A principal with unit:run-plan only starts a job that names a pool or a location in the request.
- **THEN** The server answers 403 and names the missing action.

#### Scenario: Pool-only override refused
- **WHEN** A principal with unit:define-steps but not namespace:manage-runners names only a pool in the request.
- **THEN** The server answers 403 and names namespace:manage-runners.

#### Scenario: Manual lock stops start
- **WHEN** A trigger or API start targets a unit under a manual lock.
- **THEN** The server refuses the start and names the lock.

#### Scenario: Deleted pool stays queued
- **WHEN** The snapshot pool is deleted or has no runners between queueing and running.
- **THEN** The job stays queued with no runner claim.

### Requirement: Trigger saver read check

When a job with a trigger reads the outputs of another unit, the server SHALL allow the read only when the sharing rule names the job unit and trigger.OutputReadCheck.Allow allows the last saver and the manual-fire caller.

#### Scenario: Saver loses access
- **WHEN** The last saver of the trigger loses unit:read-outputs on the source after the job starts.
- **THEN** The next job-token read of the source outputs answers 403 with no secret value.

#### Scenario: Manual fire intersection
- **WHEN** A person fires a trigger manually and the job reads outputs of another unit.
- **THEN** The server checks the sharing rule and the unit:read-outputs grants of both the last saver and the person who fired.

#### Scenario: Collection rule names job
- **WHEN** A sharing rule of the producer names the collection of the job unit.
- **THEN** The read passes check 1 when the saver check passes.

#### Scenario: Namespace rule names job
- **WHEN** A sharing rule of the producer names the namespace of the job unit.
- **THEN** The read passes check 1 when the saver check passes.

### Requirement: Start role mapping

When a principal starts a job, the server SHALL keep unit:run-plan in the planner role, grant unit:define-steps to the lead role, and keep namespace:manage-runners in the administrator role.

#### Scenario: Planner refused on override
- **WHEN** A principal with the planner role names a location or a pool in the start request.
- **THEN** The server answers 403 and names unit:define-steps or namespace:manage-runners.
