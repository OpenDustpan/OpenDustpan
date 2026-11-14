## ADDED Requirements

### Requirement: missing-namespace-disposition

When a claimed operation names a permanently missing namespace, the server SHALL end that operation after a confirming second read and write a disposition record.

#### Scenario: Missing namespace ahead of valid work
- **WHEN** an operation names a permanently missing namespace ahead of valid work
- **THEN** the server ends the operation, writes a disposition record naming the namespace and the reason, and valid work stays claimable across repeated polls

#### Scenario: Missing namespace with cancellation
- **WHEN** cancellation races a confirmed missing-namespace operation
- **THEN** valid work stays claimable

#### Scenario: Transient read failure
- **WHEN** a namespace read fails for a reason other than a confirmed missing report
- **THEN** the operation stays pending and a later poll can claim it

#### Scenario: Single missing report
- **WHEN** one read reports missing and the confirming read finds the namespace
- **THEN** the operation stays pending with no disposition record

### Requirement: cancel-comment

Where a cancel races expiry, the source comment SHALL distinguish the canceled operation from the failed job.

#### Scenario: Comment agrees with change-began case
- **WHEN** cancellation races a resource-changing step
- **THEN** the comment names the operation as canceled and the job as failed, as testExpiryDuringCancel shows

### Requirement: job-token-refusal

When a job token calls start, the server SHALL reject the call as a policy refusal.

#### Scenario: Job token with start grants
- **WHEN** a job token with start grants calls start
- **THEN** the server rejects the call with the policy-refusal class and identity denial matching holds

#### Scenario: Ordinary missing grant
- **WHEN** a caller without a start grant calls start
- **THEN** the server returns access.denied

### Requirement: plan-part-secrecy

Where plan parts are stored or restored, the server SHALL require Secret true exactly for apply parts.

#### Scenario: Wrong secrecy
- **WHEN** an apply part holds Secret false or a display part holds Secret true
- **THEN** PutPlan and CheckPlanBody reject the body

#### Scenario: Reservation restore
- **WHEN** a valid reservation without parts is restored
- **THEN** CheckPlanBody accepts the body

#### Scenario: Old body
- **WHEN** an old stored body breaks the Secret rule
- **THEN** CheckPlanBody rejects the body and recovery is a new plan

### Requirement: drift-step-allowlist

When drift steps are validated, the server SHALL select the ValidateDriftSteps allowlist from the unit tool.

#### Scenario: Matching tool
- **WHEN** plan-only steps arrive with the matching unit tool
- **THEN** ValidateDriftSteps and executor validation accept the steps

#### Scenario: Other tool or unsupported tool
- **WHEN** steps of the other tool or an unsupported tool value arrive
- **THEN** ValidateDriftSteps and executor validation reject the steps

#### Scenario: Empty and mixed lists
- **WHEN** an empty step list or a mixed-tool step list arrives
- **THEN** ValidateDriftSteps rejects the empty case and the mixed-tool case

### Requirement: chunk-namespace-reuse

While chunks are stored under one claim, the server SHALL reuse the resolved namespace ID within that claim.

#### Scenario: Many chunks
- **WHEN** many chunks are stored under one claim
- **THEN** the namespace lookup count stays constant

#### Scenario: Separate claims
- **WHEN** chunks span two claims in two namespaces
- **THEN** each claim keeps its own namespace ID

#### Scenario: Retry
- **WHEN** a chunk store is retried under the same claim
- **THEN** the retried store keeps the claim namespace ID

#### Scenario: Mid-claim delete
- **WHEN** the namespace is deleted mid-claim
- **THEN** the chunk store fails and no chunk is stored under the wrong namespace

### Requirement: test-lease

Where the durable test help runs testExtend, the test help SHALL start the case with an initial lease of 3 to 5 seconds.

#### Scenario: Extend after expiry
- **WHEN** testExtend runs on a supported store
- **THEN** Extend moves expiry and the check after the original lease passes with expiryWait unchanged
