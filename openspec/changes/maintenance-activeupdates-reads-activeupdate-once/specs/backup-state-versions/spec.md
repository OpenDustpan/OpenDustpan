## ADDED Requirements

### Requirement: Paged state-version reads

While a backup or restore copies state versions, the server SHALL read them in id-ordered pages of 200 rows.

#### Scenario: Flat backup reads
- **WHEN** backup copies state versions while maintenance holds writes
- **THEN** the round-trip count grows by at most 3 reads per 200-row page

#### Scenario: Flat restore reads
- **WHEN** restore copies state versions while maintenance holds writes
- **THEN** the restore round-trip count grows by at most 3 reads per 200-row page
