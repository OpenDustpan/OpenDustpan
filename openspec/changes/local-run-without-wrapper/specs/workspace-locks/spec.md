## ADDED Requirements

### Requirement: plain-versus-claim-queue-order

When a plain lock request races a held whole-unit lock, the server SHALL serialize the two holders in FIFO order of the whole-unit lock queue.

#### Scenario: Plain races held whole-unit lock
- **WHEN** plain run requests the lock while a runner job holds the whole-unit lock
- **THEN** server refuses the second locker or queues it in FIFO order behind the holder with no lost update
