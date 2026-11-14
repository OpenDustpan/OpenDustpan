## ADDED Requirements

### Requirement: idempotency-pass-through

When the chat sender sends the same IdempotencyKey twice, the sender SHALL return the first result without a second post.

#### Scenario: retry deduplicates
- **WHEN** the same IdempotencyKey is sent twice
- **THEN** the second send returns the first result without a second post
