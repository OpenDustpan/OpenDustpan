## ADDED Requirements

### Requirement: first-boot-mode-pinned

When an installation starts in signed-request mode, the server SHALL keep the first-boot mode unchanged and require one pinned offline public key.

#### Scenario: Start needs a pinned key
- **WHEN** an installation starts in signed-request mode with no pinned key
- **THEN** the server refuses recovery with 403 and reports missing-key on the status route.

### Requirement: signed-request-grants-session

When an installation in signed-request mode receives a fresh signed recovery request with a valid signature, the server SHALL issue one admin session.

#### Scenario: Valid request gives one session
- **WHEN** a fresh request has a valid offline-key signature
- **THEN** the server issues one session to the recovery administrator and writes one audit entry with mode, time and source.

### Requirement: wrong-signature-refused

When a recovery request carries a wrong signature or an unknown key, the server SHALL refuse it with 401.

#### Scenario: Wrong signature gives no session
- **WHEN** a request carries a wrong signature
- **THEN** the server answers 401, issues no session and writes one refused-use audit entry.

### Requirement: replay-refused

When the same recovery request ID arrives a second time, the server SHALL refuse it with 409.

#### Scenario: Second use gives no session
- **WHEN** the same ID arrives again
- **THEN** the server answers 409 and issues no second session.

#### Scenario: Crash before commit leaves it unused
- **WHEN** the server crashes before commit
- **THEN** a retry with the same ID succeeds once.

### Requirement: wrong-mode-refused

When a recovery request arrives at an installation whose recovery mode is not signed-request, the server SHALL refuse it with 403.

#### Scenario: Wrong mode gives no session
- **WHEN** the mode is not signed-request
- **THEN** the server answers 403 with code break-glass.wrong-mode and issues no session.

### Requirement: stale-request-refused

When a recovery request is past its expiry or not yet valid past the skew bound, the server SHALL refuse it with 401.

#### Scenario: Expired request gives no session
- **WHEN** the request is past its expiry
- **THEN** the server answers 401 and issues no session.

### Requirement: concurrent-consume-single-winner

When the same unused recovery request ID arrives in two concurrent submits, the server SHALL issue one admin session for one submit only.

#### Scenario: Two at-once submits give one session
- **WHEN** two submits carry one unused ID
- **THEN** one submit gets a session and the other gets 409.

### Requirement: recovery-ends-prior-sessions

When a valid signed recovery request succeeds, the server SHALL add 1 to the session generation of the recovery administrator in the same transaction.

#### Scenario: Prior cookies end
- **WHEN** a valid request succeeds
- **THEN** cookies from before no longer authenticate.

### Requirement: refused-use-bounded

When refused recovery submits pass the per-address refusal limit, the server SHALL throttle further refused submits by client address.

#### Scenario: Flood stays bounded
- **WHEN** refused submits pass the limit
- **THEN** the server throttles them and still accepts a later valid request from the same address.
