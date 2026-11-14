## ADDED Requirements

### Requirement: lease-margin

When the durable runner waits on a lease deadline, the server SHALL drive the wait from the injected clock.

#### Scenario: lease margin mutation
- **WHEN** lease approaches deadline on the fake clock
- **THEN** renewal fires at the fake deadline and a 1-tick margin mutation fails the NEW TestLeaseMargin

### Requirement: fake-advance

When the fake clock advances via Advance, the server SHALL fire due timers and tickers exactly once.

#### Scenario: big advance with reset stop
- **WHEN** one Advance spans multiple ticks
- **THEN** each ticker fires once per tick and Reset or Stop wins the race

### Requirement: wall-clock-ban

Where direct wall-clock calls remain, the server SHALL fail forbidigo except for listed exclusions.

#### Scenario: new direct call banned
- **WHEN** a new time.Now call appears outside exclusions
- **THEN** lint fails
