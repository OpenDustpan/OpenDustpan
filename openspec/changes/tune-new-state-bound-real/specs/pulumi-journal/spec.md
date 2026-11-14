## ADDED Requirements

### Requirement: Enforce new-state byte bound

When a Pulumi update carries more new state bytes than MaxNewStateBytesPerUpdate, the server SHALL reject the update with status 413.

#### Scenario: One byte over rejects
- **WHEN** A Pulumi update carries one byte more new state bytes than MaxNewStateBytesPerUpdate
- **THEN** The server rejects the update with status 413.

#### Scenario: Exactly equal accepts
- **WHEN** A Pulumi update carries exactly MaxNewStateBytesPerUpdate new state bytes
- **THEN** The server accepts the update and replays its journal.

#### Scenario: Just under accepts
- **WHEN** A Pulumi update carries one byte fewer new state bytes than MaxNewStateBytesPerUpdate
- **THEN** The server accepts the update and replays its journal.

#### Scenario: Verifier and replayer agree
- **WHEN** A Pulumi update exceeds MaxNewStateBytesPerUpdate
- **THEN** Both the verifier and the replayer reject the update.

### Requirement: Count bound from p1 state

When the corpus measurement exists, the server SHALL enforce a count bound of 33554432 divided by the p1 real resource state size in bytes, floored at 1000 and capped at 500000 new resources per update.

#### Scenario: Count over bound rejects
- **WHEN** A Pulumi update carries more new resources than the count bound derived from the p1 real state size
- **THEN** The server rejects the update with status 413.

### Requirement: Bound error text

When the server rejects a Pulumi update for the new-state bound, the server SHALL answer 413 with the limit value in bytes.

#### Scenario: 413 names limit and command
- **WHEN** The server rejects a Pulumi update for the new-state bound
- **THEN** The 413 body names the limit value in bytes with the one command that helps.

### Requirement: Forward-only bound change

While a bound change is in effect, the server SHALL apply the current bound to in-flight updates and to replays after restart.

#### Scenario: In-flight update under new bound fails
- **WHEN** A lowered bound takes effect while a journaled update that exceeds it is still in flight
- **THEN** The server rejects the in-flight update with status 413.

#### Scenario: Replay after restart uses current bound
- **WHEN** The server replays a stored journal after a restart
- **THEN** The server applies the current bound to the replayed bytes.

### Requirement: Corpus hygiene

Where the corpus file holds real provider states, the server test corpus SHALL hold only anonymized shapes and sizes with no plain secret values.

#### Scenario: No secret values in corpus
- **WHEN** The NEW corpus file holds a plain secret value, URL credential, or account name
- **THEN** The corpus check fails.
