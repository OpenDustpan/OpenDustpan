## ADDED Requirements

### Requirement: Service group reach check

When a principal saves an access limit through PutAccessLimit, the server SHALL refuse the save when the limit gives a group more reach than the saver holds.

#### Scenario: Hostile group limit
- **WHEN** A saver without the reach of a protected group saves an access limit for that group through PutAccessLimit.
- **THEN** The server returns the NEW sentinel ErrAccessLimitGroupReach, stores nothing, and audits a caller refusal.

#### Scenario: Allowed group limit
- **WHEN** A saver that holds the reach of the group saves an access limit within that reach.
- **THEN** The server stores the limit through PutAccessLimit and audits the save.

#### Scenario: Narrowing save
- **WHEN** A saver that holds the reach of the group saves a narrower limit for that group.
- **THEN** The server stores the limit through PutAccessLimit.

#### Scenario: Decision error
- **WHEN** The reach decision returns an error.
- **THEN** The server returns the NEW sentinel ErrAccessLimitDecision, stores nothing, audits the NEW reason access-limit.decision-error, and the handler answers 500.

#### Scenario: Replay path
- **WHEN** A replay saves an access limit with the starter as saver.
- **THEN** The server runs the same PutAccessLimit reach check for the principal that started the replay and refuses or stores with the same rules.

### Requirement: Handler refusal mapping

When PutAccessLimit returns a group reach sentinel on a PUT access-limit route, the server SHALL map the sentinel to its refusal status with one audit entry.

#### Scenario: Service refusal mapping
- **WHEN** PutAccessLimit returns the NEW sentinel ErrAccessLimitGroupReach on a PUT access-limit route.
- **THEN** The server answers 403 with no second audit entry.

#### Scenario: Service decision error mapping
- **WHEN** PutAccessLimit returns the NEW sentinel ErrAccessLimitDecision on a PUT access-limit route.
- **THEN** The server answers 500 and the audit entry carries the NEW reason access-limit.decision-error with no second audit entry.
