## ADDED Requirements

### Requirement: A log redaction writes the next version and shreds the older one
When a principal with `unit:redact` redacts a step log version by substitution, the server SHALL write the result as the next version with a new item key. It SHALL then redact the older version in one fenced transaction: mark it redacted, shred its item key, append the redaction record and write the audit entry. The audit entry and the job record SHALL hold the match count and the hashes, and SHALL NOT hold the given text.

#### Scenario: Redact a secret
- **GIVEN** a step log of an ended step that holds the text `hunter2` three times
- **WHEN** a principal with `unit:redact` redacts that text from the version with a reason
- **THEN** the answer carries the next version and 3 matches, the log route returns the log with `[redacted]` in place of the text, and the older version answers `secret.ErrShredded` to every read

#### Scenario: The audit entry holds no text
- **GIVEN** the redaction of `hunter2`
- **WHEN** the audit log is read
- **THEN** one entry with the action `unit:redact` names the job, both versions, 3 matches and the reason, and no field of it holds `hunter2`

#### Scenario: Both versions stay in the record
- **GIVEN** a redacted log version
- **WHEN** the job is read
- **THEN** the step record names the next version as `log` and the older version in `earlier`, each with its size, hash and time, and the older version has a `redactedAt` time

### Requirement: A failed redaction changes nothing
If the audit write, the redaction record, the item key shred or the fence check fails, then the server SHALL leave the older version readable and SHALL NOT mark it redacted.

#### Scenario: The audit write fails
- **GIVEN** a redaction whose audit write fails
- **WHEN** the redaction runs
- **THEN** the older version stays readable, its item key stays usable and no redaction record exists

#### Scenario: Fence moved
- **GIVEN** a redaction with a fence that the namespace moved past
- **WHEN** the redaction runs
- **THEN** it answers `namespace.ErrFenceStale` and writes nothing

### Requirement: A retry redacts only the older version
If the next version exists and the older version is not redacted, then a repeat of the same request SHALL redact only the older version and SHALL NOT write another version.

#### Scenario: Retry after a failure
- **GIVEN** a log with a next version and an older version that is not redacted
- **WHEN** the principal sends the request again
- **THEN** the older version is redacted, the log has no third version and the answer carries the matches of the first call

### Requirement: Refusals of a log redaction
The server SHALL refuse a redaction of the log of a step that runs with `history.ErrInProgress`, a version that is already redacted with `history.ErrRedacted`, and the current version with `history.ErrLatest`, each before any write.

#### Scenario: Open step
- **WHEN** a principal redacts the log of a step that runs
- **THEN** the answer is 409 `job.wrong-state` and nothing is written

#### Scenario: Already redacted
- **WHEN** a principal redacts a version that is redacted
- **THEN** the answer is 409 `job.wrong-state` and nothing is written

#### Scenario: Unknown version
- **WHEN** the version ID is not a log version of the job
- **THEN** the answer is 404 `job.not-found`

### Requirement: Redaction of a plan
The job artifact store SHALL redact the plan of a job that ended through `history.Redactor` with the same transaction, and SHALL refuse the plan of a job that has not ended with `history.ErrInProgress`.

#### Scenario: Plan of an ended job
- **WHEN** the plan of an ended job is redacted through the port
- **THEN** its item key is shredded, a redaction record exists and a read of the plan answers `secret.ErrShredded`

#### Scenario: Plan of a running job
- **WHEN** the plan of a job that has not ended is redacted
- **THEN** the port answers `history.ErrInProgress` and writes nothing

### Requirement: Who may redact
The server SHALL allow `unit:redact` only to a principal that holds it by a stored grant of a role of the family `data`. The built-in roles `viewer`, `writer`, `maintainer` and `planner` SHALL NOT hold it.

#### Scenario: Administrator
- **WHEN** a principal with the built-in role `admin` on the namespace redacts
- **THEN** the redaction passes the policy check

#### Scenario: Writer
- **WHEN** a principal with the role `writer` redacts
- **THEN** the answer is 403 `access.denied` and nothing is written

#### Scenario: Read only
- **WHEN** a principal with `job:read-logs` and without `unit:redact` redacts
- **THEN** the answer is 403 `access.denied`
