## ADDED Requirements

### Requirement: signed-request-grants-session

When an installation in signed-request mode receives a fresh signed recovery request with a valid signature, the server SHALL issue one admin session.

#### Scenario: Valid signed request gives one admin session
- **WHEN** an installation in signed-request mode receives a fresh request with a valid offline-key signature
- **THEN** the server issues one admin session, writes one audit entry with mode and time and source, writes the break-glass.used notice in the same transaction, and opens one break-glass review only when the review opt-in is on.

### Requirement: wrong-signature-refused

When a recovery request carries a wrong signature or an unknown key, the server SHALL refuse it with 401 and issue no session.

#### Scenario: Wrong signature gives no session
- **WHEN** a recovery request carries a wrong signature or a key the installation does not pin
- **THEN** the server answers 401, issues no session, writes one refused-use audit entry, and opens no review.

### Requirement: replay-refused

When the same recovery request ID arrives a second time, the server SHALL refuse it and issue no session.

#### Scenario: Second use gives no session
- **WHEN** the same recovery request ID arrives a second time
- **THEN** the server answers 409, issues no second session, writes one refused-use audit entry, and opens no review.

#### Scenario: Restart after commit still refuses replay
- **WHEN** the server restarts after it committed a recovery use
- **THEN** the server answers 409 after restart and issues no session.

### Requirement: wrong-mode-refused

When a recovery request arrives at an installation whose recovery mode is not signed-request, the server SHALL refuse it with 403 and issue no session.

#### Scenario: Wrong mode gives no session
- **WHEN** a recovery request arrives at an installation whose recovery mode is not signed-request
- **THEN** the server answers 403 with code break-glass.wrong-mode, issues no session, and writes one refused-use audit entry.

### Requirement: stale-request-refused

When a recovery request is past its expiry or not yet valid past the skew bound, the server SHALL refuse it with 401 and issue no session.

#### Scenario: Expired request gives no session
- **WHEN** a recovery request is past its expiry or has an issued-at time in the future past the skew bound
- **THEN** the server answers 401, issues no session, and writes one refused-use audit entry.

### Requirement: misdirected-malformed-refused

When a recovery request names another installation or audience or carries a malformed request ID, the server SHALL refuse it with 401 and issue no session.

#### Scenario: Misdirected or malformed request gives no session
- **WHEN** a recovery request names another installation or audience or carries a malformed or oversize request ID
- **THEN** the server answers 401, issues no session, and writes one refused-use audit entry.

### Requirement: concurrent-consume-single-winner

When the same unused recovery request ID arrives in two concurrent submits, the server SHALL issue one admin session for one submit only.

#### Scenario: Two at-once submits give one session
- **WHEN** the same unused recovery request ID arrives in two concurrent submits
- **THEN** the server issues one admin session for one submit, answers 409 to the other submit, and records the request ID once.

### Requirement: refused-use-bounded

When refused recovery submits pass the per-address refusal limit, the server SHALL throttle further refused submits by client address without refusing a valid signed request.

#### Scenario: Flood of bad requests stays bounded
- **WHEN** refused recovery submits pass the per-address refusal limit
- **THEN** the server throttles further refused submits by client address, writes no unbounded audit rows, and still accepts a later valid signed request from the same address.

### Requirement: recovery-ends-prior-sessions

When a valid signed recovery request succeeds, the server SHALL add 1 to the session generation of the recovery administrator in the same transaction.

#### Scenario: Recovery ends prior sessions
- **WHEN** a valid signed recovery request succeeds for the recovery administrator
- **THEN** cookies from before the recovery no longer authenticate and only the new recovery session works.
