## ADDED Requirements

### Requirement: sticky-log-hash-error

If a log read hits a hash mismatch, then the server SHALL report the same hash error on every later Read and Close of that log read.

#### Scenario: Repeated close
- **WHEN** a log read hits a hash mismatch and the caller closes twice
- **THEN** each Close reports the same hash error

#### Scenario: Close after success
- **WHEN** a log read completes without a hash mismatch
- **THEN** Close reports no error

### Requirement: restore-chunk-row-check

If a chunk row of a restore has a bad sha256 format or a job and namespace that disagree, then the server SHALL refuse the restore with the error code backup.corrupt.

#### Scenario: Bad chunk row
- **WHEN** a chunk row carries a sha256 that is not 64 lowercase hex characters
- **THEN** CheckChunkRow refuses the row and the restore aborts with backup.corrupt

#### Scenario: Mismatched chunk row
- **WHEN** a chunk row names a job of another namespace
- **THEN** CheckChunkRow refuses the row and the restore aborts with backup.corrupt
