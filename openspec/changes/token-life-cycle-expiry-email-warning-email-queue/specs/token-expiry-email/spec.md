## ADDED Requirements

### Requirement: Queue warning email

When an access token reaches a warning time, the server SHALL queue one warning email row for that token and warning time.

#### Scenario: Warning time becomes due
- **WHEN** an access token reaches a warning time
- **THEN** one row exists for that token and warning time with state queued and due time equal to the warning time

#### Scenario: Token made after warning time
- **WHEN** a token is made less than 14 or 3 days before expiry
- **THEN** the passed warning queues at once as due now

#### Scenario: Missing owner address
- **WHEN** the principal has no email at send time
- **THEN** the row moves to failed with reason missing-address and no message leaves the server

### Requirement: Single lease holder

While a warning row is sending, the server SHALL allow only the holder of its 300 second lease to send it.

#### Scenario: Lease held
- **WHEN** a send holds the row under a 300 second lease
- **THEN** only the lease holder sends for that row

#### Scenario: Crash after SMTP accept
- **WHEN** the process ends after SMTP accepts the message and before the row moves to sent
- **THEN** the row can resend after lease expiry and the audit holds two send tries

### Requirement: Skip stale token

When a queued warning becomes stale before its send, the server SHALL skip the send and record the reason.

#### Scenario: Token revoked or disabled
- **WHEN** the token is revoked, disabled or deleted before the send
- **THEN** the row moves to failed with reason token-not-usable and no message leaves the server

#### Scenario: Expiry changed or passed
- **WHEN** the token expiry changes so the warning time no longer applies or the token already expired
- **THEN** the row moves to failed with reason expiry-changed or expired and no message leaves the server

### Requirement: SMTP TLS and input checks

If the SMTP connection lacks TLS to a non-loopback host, then the server SHALL refuse it unless the plain-SMTP opt-in is on.

#### Scenario: Plain opt-in on
- **WHEN** the plain-SMTP opt-in setting is on
- **THEN** the send proceeds over the allowed plain connection

#### Scenario: Opt-in missing or not valid
- **WHEN** the opt-in value is missing or not valid
- **THEN** the dial refuses, the row moves to retry then failed, and the refusal names the relay address, the opt-in setting and the one command that changes it

#### Scenario: Bad sender input
- **WHEN** the relay is empty or the sender holds CR or LF
- **THEN** the adapter refuses with no network call

#### Scenario: Bad certificate
- **WHEN** the relay presents a certificate that fails checks
- **THEN** the dial refuses and the row moves to retry then failed

### Requirement: Retry once and record status

If an SMTP send fails, then the server SHALL retry 1 time per row after 15 minutes for a temporary error and then record failed status with the reason.

#### Scenario: Temporary 4xx failure
- **WHEN** the SMTP send fails with a temporary error
- **THEN** the row moves to retry and the sender tries once more after 15 minutes

#### Scenario: Permanent 5xx failure
- **WHEN** the relay refuses the message or the owner address with a permanent error
- **THEN** the row moves to failed with the reason and no retry runs

#### Scenario: SMTP missing
- **WHEN** installation SMTP settings are missing at send time
- **THEN** the row stays queued with reason smtp-not-configured until token expiry, then moves to failed with reason expired

### Requirement: Email content and secrets

While sending a warning email, the server SHALL address the owner at send time and keep secrets out of the message, queue rows, logs and audit entries.

#### Scenario: Send succeeds
- **WHEN** the sender delivers a queued warning
- **THEN** the message goes to the owner address at send time with the token name and expiry date and holds no token secret or SMTP password

#### Scenario: Send fails
- **WHEN** the sender records a failure
- **THEN** the queue row, the log line and the audit entry hold no password or token secret

### Requirement: Audit of warning send

When the sender delivers, skips or fails a queued warning, the server SHALL write one audit entry for that warning.

#### Scenario: Warning sent or skipped
- **WHEN** the sender delivers, skips or fails a queued warning
- **THEN** the audit log holds one entry with the token ID, the warning time and the result
