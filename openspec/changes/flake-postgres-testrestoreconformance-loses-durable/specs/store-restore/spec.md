## ADDED Requirements

### Requirement: restore-claim-survives-slow-renewal

While the restore conformance suite runs under CPU load, the server SHALL keep the restore durable claim when one renewal is slow.

#### Scenario: restore-claim-survives-slow-renewal
- **WHEN** The restore conformance suite runs under CPU load and one claim renewal is slow.
- **THEN** The restore completes without a lease deadline failure.
