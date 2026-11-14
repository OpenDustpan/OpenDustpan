## ADDED Requirements

### Requirement: Serve claim base

When a job with an accepted claim reads state, the server SHALL return the state version named by the claim.

#### Scenario: Read under claim returns base
- **WHEN** a job with an accepted claim reads workspace state
- **THEN** the server returns the state version named by the claim, not the latest state version

### Requirement: Read without claim

When a principal reads workspace state without a claim, the server SHALL return the latest state version.

#### Scenario: Read without claim returns latest
- **WHEN** a principal reads without a claim
- **THEN** the server returns the latest state version

#### Scenario: Pruned base fails closed
- **WHEN** a claim names a base that is pruned or deleted
- **THEN** the server refuses with base-superseded and creates no state

### Requirement: Merge disjoint uploads

When a job uploads state under an accepted claim, the server SHALL merge the upload one resource instance at a time onto the current state version.

#### Scenario: Two disjoint uploads merge without loss
- **WHEN** two accepted claims upload disjoint resource instance changes
- **THEN** the final state holds the changed instances of both uploads and no other instance changes

#### Scenario: Double-changed instance is refused
- **WHEN** an upload changes an instance already changed after its base
- **THEN** the server refuses the later upload with instance-conflict and keeps current state unchanged

#### Scenario: Delete counts as change
- **WHEN** one job deletes an instance that another job changed after the shared base
- **THEN** the server refuses the later upload with instance-conflict

### Requirement: Non-instance fields

Where an upload changes root outputs, lineage, terraform version, or check results beyond resource instances, the server SHALL refuse a per-instance merge.

#### Scenario: Root outputs change takes whole-unit path
- **WHEN** an upload changes root outputs
- **THEN** the server holds the whole-unit lock and refuses a per-instance merge

#### Scenario: Replacement is conflicting
- **WHEN** two uploads replace the same instance from one base
- **THEN** the server treats the delete and the create as changes to the same instance and refuses the later upload

#### Scenario: Lineage or version drift takes whole-unit path
- **WHEN** an upload carries a lineage or terraform version different from current state
- **THEN** the server holds the whole-unit lock and refuses a per-instance merge

### Requirement: Atomic serial commit

When the server merges an upload under a claim, the server SHALL commit the merged state version with the next serial in one fenced transaction with compare-and-swap on the current serial.

#### Scenario: Failed merge writes nothing
- **WHEN** a merge under a claim fails its checks
- **THEN** no new state version appears and the current serial stays unchanged

#### Scenario: Concurrent commits elect one winner
- **WHEN** two merges commit from the same current serial
- **THEN** one commit wins with the next serial and the other fails with instance-conflict for retry from latest

#### Scenario: Crash mid-merge leaves no half state
- **WHEN** the server crashes mid-merge
- **THEN** restart shows the prior current version with no new version and no partial version

### Requirement: Fence and claim check

While a claim is active, the server SHALL reject a state write that carries a mismatched fence for the workspace.

#### Scenario: Mismatched fence is rejected
- **WHEN** a state write carries a mismatched fence for the workspace
- **THEN** the server rejects the write and keeps the current state version unchanged

#### Scenario: Expired claim is rejected
- **WHEN** a claim expires between read and commit
- **THEN** the server rejects the commit with claim-expired and keeps current state unchanged

#### Scenario: Foreign claim is rejected
- **WHEN** an upload carries a claim of another workspace
- **THEN** the server rejects the write with no state change

### Requirement: Superseded base

If an upload under a claim names a base older than the current state version, then the server SHALL refuse the upload when its instance set intersects a later change.

#### Scenario: Superseded base is refused
- **WHEN** an upload names a base serial below the current serial with a changed instance in its set
- **THEN** the server refuses the upload with base-superseded and creates no state version

### Requirement: Empty upload

If an upload under a claim holds no resource instance change, then the server SHALL reject the upload without creating a state version.

#### Scenario: Empty upload creates nothing
- **WHEN** an upload under a claim holds no resource instance change
- **THEN** the server rejects the upload without creating a state version

### Requirement: Hostile upload

If an upload under a claim holds malformed state, a lineage mismatch, or an instance outside the claim, then the server SHALL reject the upload without creating a state version.

#### Scenario: Malformed state is rejected
- **WHEN** an upload holds invalid state JSON
- **THEN** the server rejects the upload with bad-state and creates no state version

#### Scenario: Lineage mismatch is rejected
- **WHEN** an upload under a claim carries a lineage different from the workspace lineage
- **THEN** the server rejects the upload and keeps current state unchanged

#### Scenario: Outside-claim instance is rejected
- **WHEN** an upload touches an instance outside its claim
- **THEN** the server rejects the upload with no state change

### Requirement: Strict whole-unit cases

Where a strict whole-unit case applies to a run, the server SHALL hold the whole-unit lock for the run.

#### Scenario: Data source run holds lock
- **WHEN** a run with a data source block in its plan starts while an apply runs
- **THEN** the server answers 423 whole-unit-locked and the run starts its apply only after the running apply ends

#### Scenario: Provider from resources holds lock
- **WHEN** a run uses a provider with settings from resources
- **THEN** the server answers 423 whole-unit-locked until the running apply ends

#### Scenario: Root outputs change holds lock
- **WHEN** a plan changes root outputs
- **THEN** the server answers 423 whole-unit-locked until the running apply ends

#### Scenario: Client-encrypted state holds lock
- **WHEN** a run uses client-encrypted state
- **THEN** the server answers 423 whole-unit-locked until the running apply ends

#### Scenario: Version mismatch holds lock
- **WHEN** a run reports a version or lineage mismatch with current state
- **THEN** the server answers 423 whole-unit-locked until the running apply ends

#### Scenario: Local run without wrapper holds lock
- **WHEN** a local run without a job credential writes while a runner apply runs
- **THEN** the server answers 423 whole-unit-locked and a concurrent runner job waits

### Requirement: Capability retry and force

When a client retries a finalized upload with the same capability and the same content, the server SHALL return the published version without creating a second version.

#### Scenario: Same capability retry succeeds
- **WHEN** a client retries a finalized upload with the same capability and the same content
- **THEN** the retry returns the published version with no second version

#### Scenario: Force upload keeps fence
- **WHEN** a forced write arrives under a claim
- **THEN** the server skips lineage and serial checks and still checks the fence and the lock

#### Scenario: Refused merge leaves orphan-free state
- **WHEN** a merge is refused after its blob write
- **THEN** no version appears and no blob remains reachable from state after the attempt deadline

### Requirement: Lock release

When a whole-unit lock holder dies without release, the server SHALL release the lock after its lease expires.

#### Scenario: Dead holder lock expires
- **WHEN** a whole-unit lock holder dies without release
- **THEN** the server releases the lock after its lease and lets the next waiter proceed
