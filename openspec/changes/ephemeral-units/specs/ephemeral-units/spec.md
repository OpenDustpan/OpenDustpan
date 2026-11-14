## ADDED Requirements

### Requirement: Time to live on create

When a Developer requests an ephemeral unit from a blueprint, the server SHALL store its expiry time normalized to Unix milliseconds.

#### Scenario: Valid time to live
- **WHEN** a Developer requests a unit from a blueprint with a time to live inside 30 days
- **THEN** the server stores the expiry and returns it in the unit read.

#### Scenario: Missing time to live
- **WHEN** a Developer requests an ephemeral unit without a time to live
- **THEN** the server refuses the request with 422 time-to-live required.

#### Scenario: Past expiry refused
- **WHEN** a Developer requests an ephemeral unit with an expiry time in the past
- **THEN** the server refuses the request with 422 time-to-live in past.

#### Scenario: Zero time to live refused
- **WHEN** a Developer requests an ephemeral unit with zero or negative time to live
- **THEN** the server refuses the request with 422 time-to-live required.

#### Scenario: Expiry stored normalized
- **WHEN** a Developer requests an ephemeral unit with a valid time to live
- **THEN** the unit read returns the expiry as Unix milliseconds.

### Requirement: Warning before expiry

When an ephemeral unit reaches 24 hours before expiry, the server SHALL send one warning with an idempotency key to the webhook sink of the namespace.

#### Scenario: Warning sent
- **WHEN** an ephemeral unit reaches 24 hours before expiry
- **THEN** the server sends one warning with extend and approve next steps to the webhook sink before expiry.

#### Scenario: Warning sent once with retry
- **WHEN** the webhook sink is down at the first warning time
- **THEN** the server retries until sink accept and the sink dedupes by idempotency key so the unit notifies once.

#### Scenario: Short TTL warns at create
- **WHEN** a unit is created with a time to live below 24 hours
- **THEN** the server sends the warning at once with the same idempotency key.

### Requirement: Destroy at expiry

When an ephemeral unit reaches its expiry time, the server SHALL create a destroy job on a runner in the same transaction as the expiry state change.

#### Scenario: Destroy runs
- **WHEN** an ephemeral unit reaches its expiry time and no approval rule blocks it
- **THEN** the server starts a destroy job on a runner and the unit leaves the unit list after the job completes.

#### Scenario: Destroy waits
- **WHEN** an ephemeral unit with an approval rule reaches its expiry time
- **THEN** the server holds the destroy in approval wait until the review request approves it and the destroy never self approves.

#### Scenario: Destroy held returns 409
- **WHEN** a caller reads a destroy that an approval rule holds
- **THEN** the destroy status returns 409 approval wait with the review request and the next step.

#### Scenario: Destroy unclaimed or failed
- **WHEN** no runner claims the destroy job or the destroy job fails
- **THEN** the unit stays past expiry with state expiry-expired and the failed or unclaimed job visible in the unit read.

#### Scenario: Failed destroy retries
- **WHEN** the destroy job of an expired unit fails
- **THEN** the server starts a new destroy job with the same expiry reason and links it to the unit.

#### Scenario: Hand deleted unit skips destroy
- **WHEN** an ephemeral unit is already deleted by hand at expiry time
- **THEN** the server records expiry-complete with no new job.

#### Scenario: Restart mid destroy resumes
- **WHEN** the server restarts in the middle of a destroy at expiry
- **THEN** the server resumes the destroy job from stored state with no second job.

#### Scenario: Past expiry at start
- **WHEN** the server starts with a unit already past expiry
- **THEN** the server marks the unit expiry-expired and starts the destroy job path.

### Requirement: Review request holds waiting destroy

Where a destroy waits for approval, the server SHALL hold the destroy until the review request of the unit approves it.

#### Scenario: Review approval releases
- **WHEN** the review request of the expired unit is approved by a principal other than the requester
- **THEN** the server releases the destroy to a runner.

#### Scenario: Non approver refused
- **WHEN** a principal without the approve grant acts on the review request
- **THEN** the server refuses with 403 and names the review request approve command.

#### Scenario: Self approval refused
- **WHEN** the requester of the unit approves the waiting destroy
- **THEN** the server refuses with 403 self approval denied.

#### Scenario: Cancel waiting destroy
- **WHEN** a principal with manage on the unit cancels the waiting destroy
- **THEN** the server cancels the waiting destroy and marks the unit expiry-cancelled.

#### Scenario: Extend then approve completes
- **WHEN** a principal with manage on the unit extends a unit in approval wait inside the maximum
- **THEN** the server cancels the waiting destroy and applies the new expiry time.

### Requirement: Extend time to live

When a principal with the manage action on the unit extends the time to live inside 30 days from create plus one 7 day extension, the server SHALL store the new expiry time.

#### Scenario: Extend within maximum
- **WHEN** a principal with manage on the unit extends inside 30 days from create plus one 7 day extension
- **THEN** the server stores the new expiry time and returns it in the unit read.

#### Scenario: Extend past maximum refused
- **WHEN** a principal with manage on the unit extends past the maximum
- **THEN** the server refuses the request with 422 time-to-live past maximum.

#### Scenario: Extend during destroy refused
- **WHEN** a principal extends a unit past expiry with a destroy job already created
- **THEN** the server refuses the request with 409 destroy in progress.

#### Scenario: Remove expiry
- **WHEN** a principal with manage on the unit removes expiry before the destroy job is created
- **THEN** the server clears the expiry and marks the unit non-ephemeral.

### Requirement: Unit list shows expiry

When a principal lists units, the server SHALL return the expiry time of each ephemeral unit.

#### Scenario: List shows expiry
- **WHEN** a principal lists units in the namespace
- **THEN** each ephemeral unit entry carries its expiry time in Unix milliseconds.

### Requirement: Authorization of ephemeral endpoints

Where ephemeral endpoints run, the server SHALL check the declared authorization action on the named object before the action.

#### Scenario: Actions declared
- **WHEN** a principal calls the create, extend, or review request endpoint of an ephemeral unit
- **THEN** the server checks create on the collection for create, manage on the unit for extend and remove, and approve on the unit for the review request.

### Requirement: Audit of expiry destroy

When the server starts or approves a destroy at expiry, the server SHALL write an audit entry with the actor, the job ID, and the reason expiry.

#### Scenario: Destroy audit written
- **WHEN** the server starts or approves a destroy at expiry
- **THEN** the audit log holds actor expiry, job ID, and reason expiry for the start and for each approval.
