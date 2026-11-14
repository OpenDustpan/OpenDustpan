## ADDED Requirements

### Requirement: refusal audit entry

When FindCommit refuses a commit with forge.ErrUntrustedCommit, the server SHALL append one security audit entry with the job, the repository, the commit ID, and the failed check.

#### Scenario: Forged trailer commit
- **WHEN** FindCommit refuses the newest trailer commit with forge.ErrUntrustedCommit
- **THEN** the audit log holds one security entry with job, repository, commit ID, and failed check, with no host text, and no delivered record exists.

#### Scenario: Hostile trailer text
- **WHEN** the trailer holds control characters
- **THEN** the security entry holds the commit ID and failed check with no host text copied from the commit.

#### Scenario: Refusal is idempotent
- **WHEN** the caller step resumes after a stop after the first append
- **THEN** the audit log still holds one security entry for that job and commit.

### Requirement: refused takeover fails

When the NEW RefuseTakeover step observes forge.ErrUntrustedCommit, the server SHALL fail the job with the failed check reason and no retry.

#### Scenario: Forged trailer commit stops takeover
- **WHEN** the NEW RefuseTakeover step observes forge.ErrUntrustedCommit
- **THEN** the job state is failed with the failed check reason and no retry starts.

#### Scenario: Audit store unavailable
- **WHEN** the security entry append fails after the refusal
- **THEN** the job still moves to failed with no retry and the step retries only the audit append on resume.

#### Scenario: Reason visibility
- **WHEN** a holder of the existing jobs read grant reads the refused job
- **THEN** the jobs read answer shows the failed check name to that holder and denies any other namespace or job credential.

### Requirement: proof binds digest branch parent

Where the write-back proof is sealed with SealBound, the server SHALL bind the change digest and the branch name.

#### Scenario: Same trailer other content refused
- **WHEN** a commit carries the trailer with other changes than the bound digest
- **THEN** FindCommit returns forge.ErrUntrustedCommit.

#### Scenario: Same trailer other branch refused
- **WHEN** a commit carries the trailer on another branch than the bound branch
- **THEN** FindCommit returns forge.ErrUntrustedCommit.

#### Scenario: Parent is not base refused
- **WHEN** the found commit parent does not equal the base commit
- **THEN** FindCommit returns forge.ErrUntrustedCommit.

#### Scenario: Old binding refused pre-tag
- **WHEN** a commit carries a proof sealed with the old binding without digest and branch
- **THEN** FindCommit returns forge.ErrUntrustedCommit.

### Requirement: prover port location

Where a forge adapter seals a write-back proof, the server SHALL provide the Prover port from pkg/forge.

#### Scenario: Shared prover port
- **WHEN** a forge adapter seals a write-back proof with SealBound
- **THEN** the GitHub adapter seals and opens proofs through the Prover port in pkg/forge.
