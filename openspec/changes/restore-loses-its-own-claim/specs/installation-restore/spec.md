## ADDED Requirements

### Requirement: Keep claim during slow PostgreSQL swap

When the PostgreSQL swap lock wait exceeds the renewal interval, the server SHALL keep the restore claim through the in-transaction pre-extend of keepControlTables.

#### Scenario: Slow swap keeps claim
- **WHEN** a swap lock wait of 60 s exceeds the renewal interval of 10 s
- **THEN** the swap runs 1 time and the restore completes with 0 claim_lost events

### Requirement: Keep claim during slow SQLite swap

When the SQLite swap block exceeds the renewal interval, the server SHALL keep the restore claim through the in-transaction pre-extend of copyControlTables.

#### Scenario: Slow SQLite swap keeps claim
- **WHEN** a SQLite swap block of 60 s exceeds the renewal interval of 10 s
- **THEN** the swap runs 1 time and the restore completes with 0 claim_lost events

### Requirement: Record failed swap attempt

When the swap ends with a deadlock, the server SHALL record the failed attempt with its SQLSTATE in the operation record.

#### Scenario: Deadlock records reason
- **WHEN** the swap ends with a deadlock
- **THEN** the operation record holds 1 failed attempt with SQLSTATE 40P01 and the swap retries at most 1 more time

### Requirement: Show effective swap lease

When the swap pre-extends the lease, the server SHALL report the extended lease deadline in the operation record.

#### Scenario: Effective lease visible
- **WHEN** the swap pre-extends the lease before LOCK TABLE
- **THEN** the operation record shows the extended lease deadline

### Requirement: Fail closed after bound

If the swap fails twice in a row, then the server SHALL end the restore with restore.swapped through EndDurableClaim.

#### Scenario: Second swap failure ends restore
- **WHEN** the swap fails twice in a row
- **THEN** the restore ends with restore.swapped and EndDurableClaim closes the claim

#### Scenario: Foreign claim denied
- **WHEN** a caller presents a claim that is not the restore claim
- **THEN** the pre-extend is denied and the swap does not start
