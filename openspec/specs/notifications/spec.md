# notifications Specification

## Purpose
The server sends notifications about events to destinations that an operator sets. This capability covers how a refused redelivery counts against the shared denial limiter. It does not cover the senders or the destination settings.

## Requirements

### Requirement: Repeated redeliver refusals go through the denial limiter

When the server refuses a redeliver at the limit or too soon, it SHALL record the refusal through the shared denial limiter: the first refusal of a key (principal, action, object kind, scope) in the window writes one denied entry, and later refusals of that key are counted and reported with the next entry.

#### Scenario: N identical refusals
- **WHEN** one person sends N redelivers of one delivery that are refused at the limit in one window
- **THEN** the audit log holds one denied entry for them, and the next entry of that key carries a count of N-1

#### Scenario: Another key
- **WHEN** another person, or a delivery of another namespace, is refused in the same window
- **THEN** that refusal writes its own denied entry
