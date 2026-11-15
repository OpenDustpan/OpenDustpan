## ADDED Requirements

### Requirement: A too-soon redeliver refusal is audited

When the server refuses a redeliver of a notification delivery because it comes too soon, it SHALL record the refusal through the shared denial limiter, so that the first refusal of a key in the window writes one denied audit entry with the reason `sink.delivery-too-soon` and later ones are counted.

#### Scenario: A redeliver comes too soon
- **WHEN** a person asks to send a delivery again before its wait ends
- **THEN** the server answers 429 with `sink.delivery-too-soon` and `Retry-After`, changes no delivery, and the audit log of the namespace holds one denied entry for the refusal

#### Scenario: The same refusal again in the window
- **WHEN** the same person sends the same too-soon redeliver again in the same minute
- **THEN** the server writes no second entry, and the next entry carries the count
