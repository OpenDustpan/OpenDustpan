## ADDED Requirements

### Requirement: Staged file sweep

While the registry sweep is due, the server SHALL delete each staged file that is older than 24 hours on the database clock and that no publish moved, in every namespace.

#### Scenario: Old staged file removed
- **WHEN** the sweep runs over a namespace with a staged file older than 24 hours and no moved flag
- **THEN** the staged key is gone and the staged delete counter grows by one

#### Scenario: Young staged file kept
- **WHEN** a staged file is 23 hours old
- **THEN** the sweep keeps the file

#### Scenario: Publish during the sweep
- **WHEN** a publish moves a staged file after the sweep lists it
- **THEN** the sweep reads the moved flag and keeps the file

#### Scenario: Staged key with no row
- **WHEN** a staged key has no row and its listing age is above 24 hours
- **THEN** the sweep deletes the key as an unknown key

### Requirement: Unreferenced content sweep

While the registry sweep is due, the server SHALL delete each content blob older than 24 hours that no live release references, in every namespace.

#### Scenario: Shared blob kept
- **WHEN** one content hash has a live release reference and another hash has none
- **THEN** the shared blob stays and the unreferenced blob is gone

#### Scenario: Publish with no mark yet
- **WHEN** a publish starts before the sweep writes a mark and commits a reference to the blob
- **THEN** the sweep sees the reference and keeps the blob

#### Scenario: Release written during the sweep
- **WHEN** a publish references a blob that the sweep marked for delete
- **THEN** the publish waits for the delete or the mark expiry, copies the blob again and the release stays readable

#### Scenario: Fresh unknown key kept
- **WHEN** a key under the registry prefix has no row and is 1 hour old
- **THEN** the sweep keeps the key

### Requirement: Mark expiry

If a delete mark is older than 1 hour on the database clock, then the server SHALL ignore the mark and remove it.

#### Scenario: Stale mark
- **WHEN** the sweep crashed after it wrote a mark
- **THEN** a later publish copies the blob and the next run removes the mark

#### Scenario: Failed store delete
- **WHEN** the store delete of a marked blob fails
- **THEN** the mark stays until it expires and the next run tries the delete again

#### Scenario: Failed mark removal
- **WHEN** the store delete succeeds and the mark removal fails
- **THEN** the mark expires after 1 hour and the next run removes it

### Requirement: Resume after takeover

If another instance takes over the sweep run, then the server SHALL resume from the last recorded key and check the fencing token before each delete.

#### Scenario: Takeover mid-run
- **WHEN** an instance stops in the middle of a run and another instance claims it
- **THEN** the new instance continues after the last key and the old instance deletes nothing more

### Requirement: Maintenance pause

While the installation is in maintenance mode, the server SHALL make no delete in the registry sweep.

#### Scenario: Sweep during restore
- **WHEN** the schedule is due in maintenance mode
- **THEN** no key is deleted and the run starts again after maintenance ends

#### Scenario: Restored blob kept
- **WHEN** a restore wrote a blob and its release row, and the sweep runs after maintenance ends
- **THEN** the sweep keeps the blob because a live release references it

### Requirement: Store error retry

If the blob store returns an error during a listing, then the server SHALL retry the sweep step with backoff.

#### Scenario: Listing error
- **WHEN** a page of the listing fails
- **THEN** the run retries the page later and deletes no key from the failed page

### Requirement: Paged listing

When a blob store lists more keys than one page holds, the server SHALL visit every key once in the sweep.

#### Scenario: Many keys
- **WHEN** the store holds more than 10000 keys under the registry prefix
- **THEN** the sweep visits each key once and deletes only the keys that the rules allow

### Requirement: Sweep metric

When a registry sweep run completes, the server SHALL expose the staged deleted count, the content deleted count and the last run time for the installation.

#### Scenario: Counters visible
- **WHEN** a run completes with deletions and a run completes with none
- **THEN** the metric shows both counts and the last run time
