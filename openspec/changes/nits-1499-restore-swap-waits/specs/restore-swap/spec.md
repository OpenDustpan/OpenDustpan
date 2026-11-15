## ADDED Requirements

### Requirement: Visible swap wait

While a restore apply waits for the swap lock, the server SHALL log one warning per holder after 500 ms of waiting.

#### Scenario: Swap waits past 500 ms
- **WHEN** a restore apply waits more than 500 ms behind a backup snapshot read
- **THEN** the server logs one warning and applies after the read ends

#### Scenario: Short wait
- **WHEN** a restore apply wins the swap lock before 500 ms
- **THEN** the server logs no warning

#### Scenario: Retry logs once
- **WHEN** the same holder retries the swap after 500 ms
- **THEN** the server logs no second warning for the same holder
