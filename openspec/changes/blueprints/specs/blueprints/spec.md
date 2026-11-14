## ADDED Requirements

### Requirement: Publish blueprint

When a platform team publishes a blueprint version, the server SHALL store the version in the same namespace.

#### Scenario: Publish then list
- **WHEN** a platform team publishes a blueprint version for Pulumi and one for OpenTofu
- **THEN** the server stores the version and lists it in the namespace

#### Scenario: Other namespace refused
- **WHEN** a request names a blueprint of another namespace or a missing name
- **THEN** the server answers 404 self-service.blueprint-not-found as for a name that does not exist

#### Scenario: Bad schema refused
- **WHEN** a publish holds an invalid schema, more than 16 quota rules, or a window outside 1 minute to 31 days
- **THEN** the server refuses the publish with a 400 error that names the fault

#### Scenario: Lower limit keeps units
- **WHEN** a new version lowers max below current usage
- **THEN** the server keeps all units and refuses new creates until usage drops

### Requirement: Request unit

When a developer submits a valid Self-service request, the server SHALL make one unit in its collection and start one job.

#### Scenario: Valid request applies
- **WHEN** another team submits a valid Self-service request
- **THEN** the server makes the unit in its collection and starts one job that applies it on a runner with pulumi up or tofu apply

### Requirement: Idempotent submit

When a client submits a Self-service request with an Idempotency-Key header, the server SHALL return one revision for one key.

#### Scenario: Retry with same key gives one revision
- **WHEN** a client retries POST self-service requests with the same Idempotency-Key after a network failure
- **THEN** the server returns the same revision, unit and job without a second reservation or a second unit

### Requirement: Refuse bad input

If a request holds an input that the blueprint does not allow, then the server SHALL refuse the request with an error that names the input.

#### Scenario: Unknown input refused
- **WHEN** a request holds an input that the blueprint does not allow
- **THEN** the server refuses the request with a 400 error that names the input and makes no unit and no job

### Requirement: Refuse past quota

If a create exceeds a quota rule, then the server SHALL refuse the create and make no unit and no job.

#### Scenario: Units quota refused
- **WHEN** a create exceeds a units quota rule
- **THEN** the server refuses the create with a 409 quota error and makes no unit and no job

#### Scenario: Requests quota refused
- **WHEN** a submit exceeds a requests quota rule in its window
- **THEN** the server refuses the submit and stores no revision

### Requirement: Concurrent submit

While a request has one revision in flight, the server SHALL refuse a second submit and keep idempotency by request ID and revision.

#### Scenario: Second submit refused
- **WHEN** a second submit arrives for a request with a revision in flight
- **THEN** the server refuses the second submit while one revision is in flight

#### Scenario: Retry gives one unit and one job
- **WHEN** a client retries a submit with the same request ID and revision after a crash between reserve and unit make
- **THEN** the server returns the same revision, unit and job without a second reservation

#### Scenario: Last slot gives one winner
- **WHEN** two callers submit at the last free unit slot at the same time
- **THEN** one submit succeeds and the other fails with 409 and no extra unit

### Requirement: Deny without action

If a caller lacks the request action, then the server SHALL deny the submit.

#### Scenario: Submit without action denied
- **WHEN** a caller without the request action submits a valid document
- **THEN** the server denies the call and writes an audit entry

### Requirement: Safe render

When the server renders a revision, the server SHALL encode inputs as plain data with no secret value.

#### Scenario: Hostile input stays data
- **WHEN** inputs hold shell metacharacters and template markers
- **THEN** the rendered files hold the markers as plain data and hold no secret value

#### Scenario: Same revision renders same bytes
- **WHEN** the server renders one revision twice
- **THEN** both renders give identical bytes with no secret value

### Requirement: Quota settle and release

When a revision ends, the server SHALL settle or release its reservation as ADR 115 defines.

#### Scenario: Failed create keeps unit
- **WHEN** a failed create wrote a state version
- **THEN** the server settles the reservation and keeps the unit count

#### Scenario: Rejected revision releases
- **WHEN** a revision ends rejected, canceled, failed or expired
- **THEN** the server deletes the reservation and frees the slot

### Requirement: Quota recheck at approval and apply

When a revision reaches approval or apply, the server SHALL recheck quota rules and refuse on failure.

#### Scenario: Failing recheck releases
- **WHEN** a units rule fails at approval or at apply
- **THEN** the server refuses the dispatch and releases the reservation

#### Scenario: Requests rule at submit only
- **WHEN** a revision reaches approval with a requests quota rule
- **THEN** the server checks the requests rule once and does not recheck it later

### Requirement: Provision then start failure

If the job start fails after the unit make, then the server SHALL keep the idempotent unit and report the failure for retry.

#### Scenario: Job start fails after unit make
- **WHEN** the provisioner makes the unit but the job start fails
- **THEN** the server keeps the idempotent unit and reports the job error for retry with the same key

#### Scenario: Name collision refused
- **WHEN** the computed unit name already exists in the namespace
- **THEN** the server answers 409 and keeps the existing unit unchanged

### Requirement: Reservation deadline and reconcile restart

When a reservation passes its deadline, the server SHALL release it.

#### Scenario: Expired revision releases
- **WHEN** a revision passes its deadline without an end state
- **THEN** the server deletes the reservation and frees the slot

#### Scenario: Restart reconciles counts
- **WHEN** the server restarts before the hourly reconcile
- **THEN** the server counts units again from inventory and the inventory count wins with a log entry and a metric
