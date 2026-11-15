## ADDED Requirements

### Requirement: benchmark-note-content

The benchmark note SHALL report method, per-pattern numbers, and one recommended hold pattern.

#### Scenario: Reader compares three patterns
- **WHEN** A reader opens the benchmark note
- **THEN** The note shows one table per pattern with method, token ledger counts, peak memory, resume time, and one recommended pattern.

#### Scenario: Reader checks scale rows
- **WHEN** A reader checks scale in the benchmark note
- **THEN** The note shows peak memory and poll load for 1 and 50 concurrent waiting runs for each pattern.

### Requirement: benchmark-note-failure-rows

The benchmark note SHALL record restart, cancel, limit, expiry, and repeat behavior for each hold pattern.

#### Scenario: Reader checks restart and cancel behavior
- **WHEN** A reader checks failure behavior in the benchmark note
- **THEN** The note shows what resumes, what repeats, what is lost, and whether a repeated proposing step yields one change for each gap, restart step, takeover and cancel row.

#### Scenario: Reader checks credential rows
- **WHEN** A reader checks credential behavior in the benchmark note
- **THEN** The note shows the result of a credential expiry and of a revocation during a wait for each pattern.

#### Scenario: Reader checks limit rows
- **WHEN** A reader checks limit behavior in the benchmark note
- **THEN** The note shows what happens to a run whose wait passes the time limit, for each pattern.
