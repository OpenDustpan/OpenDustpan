## ADDED Requirements

### Requirement: Lag gates readiness

While the shared audit sink cursor lag passes 300 s, the server SHALL report not ready with reason sink-lag.

#### Scenario: Lag marks not ready
- **WHEN** shared sink cursor lag passes 300 s
- **THEN** readiness reports not ready with reason sink-lag until lag clears plus 30 s debounce

#### Scenario: Chain gate feeds readiness
- **WHEN** audit chain gate reports invalid or unknown
- **THEN** readiness reports not ready with reason chain-gate

#### Scenario: Flap holds not ready
- **WHEN** shared cursor lag clears within 30 s of a not-ready verdict
- **THEN** readiness stays not ready for 30 s after clear

### Requirement: Bound visible

Where audit sink lag applies, the server SHALL expose 300 s as the effective value of the NEW setting `audit-sink-max-lag-seconds`.

#### Scenario: Effective bound shown
- **WHEN** console reads capabilities during sink lag
- **THEN** capabilities answer holds 300 s as effective value of the NEW setting `audit-sink-max-lag-seconds`
