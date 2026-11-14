## ADDED Requirements

### Requirement: Own drop step

When the restore swap commits, the server SHALL run DropOldLiveSchema in its own transaction with SET LOCAL lock_timeout.

#### Scenario: Held lock retries
- **WHEN** lock on old live schema blocks DROP past 5000 ms lock_timeout
- **THEN** step reports deferred with backoff and restore stays in dropping

#### Scenario: Second run no-op
- **WHEN** DropOldLiveSchema runs again after old schema is gone
- **THEN** second run changes nothing and reports done

#### Scenario: Missing staging name fails closed
- **WHEN** NEW field `staging_schema` in restores row is empty or unknown
- **THEN** step fails closed with no DROP and an audit entry

#### Scenario: Stale fence refuses
- **WHEN** drop step presents a stale fencing token
- **THEN** store refuses the DROP and next worker reclaims after expiry

#### Scenario: Wrong schema guard
- **WHEN** requested schema name equals live schema name
- **THEN** store refuses the DROP before execution

### Requirement: Step order

When a restore lists its steps through RestoreSteps, the server SHALL place the drop step after the swap step.

#### Scenario: Drop follows swap
- **WHEN** RestoreSteps lists steps of a restore with a committed swap
- **THEN** drop runs after swap and before restore done
