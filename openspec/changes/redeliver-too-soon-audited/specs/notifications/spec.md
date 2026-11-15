## ADDED Requirements

### Requirement: A too-soon redeliver refusal is audited

When the server refuses a redeliver of a notification delivery because it comes too soon, it SHALL write one denied audit entry in the namespace with the reason `sink.delivery-too-soon`, and the redeliver SHALL stay refused when that write fails.

#### Scenario: A redeliver comes too soon
- **WHEN** a person asks to send a delivery again before its wait ends
- **THEN** the server answers 429 with `sink.delivery-too-soon` and `Retry-After`, changes no delivery, and the audit log of the namespace holds one denied entry for the refusal

#### Scenario: The audit write fails
- **WHEN** a too-soon redeliver is refused and the audit write fails
- **THEN** the redeliver stays refused and the error names the failed audit write
