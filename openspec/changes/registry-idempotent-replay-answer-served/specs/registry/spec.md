## ADDED Requirements

### Requirement: Replay first publish answer

When a publish repeats with an Idempotency-Key that the server already stored, the server SHALL return the stored publisher subject string and time of the first publish.

#### Scenario: Repeat publish with same key
- **WHEN** PublishRelease repeats with a stored Idempotency-Key, same caller, same namespace and same body fingerprint
- **THEN** the answer carries the publisher subject string and time of the first publish and creates no new release

#### Scenario: Replay after principal renamed or removed
- **WHEN** PublishRelease repeats after the first publisher principal was renamed or removed
- **THEN** the answer carries the stored publisher subject string byte for byte

#### Scenario: Expired replay record runs new publish
- **WHEN** PublishRelease repeats after the stored Idempotency-Key record expired under existing retention
- **THEN** the server runs a new publish and stores a new answer

### Requirement: Fingerprint mismatch refused

When a publish repeats with a stored Idempotency-Key but another body fingerprint, caller or namespace, the server SHALL refuse the repeat with a 409 conflict.

#### Scenario: Same key another body
- **WHEN** PublishRelease repeats with the same key but another body fingerprint
- **THEN** the server refuses with a 409 conflict and creates no release

#### Scenario: Cross-caller replay refused
- **WHEN** another caller repeats the same Idempotency-Key value
- **THEN** the server refuses with a 409 conflict and never returns the stored answer of the first caller

### Requirement: Concurrent repeat single release

While a first publish with an Idempotency-Key is still running, the server SHALL return the first answer to a concurrent repeat with the same key, caller, namespace and body fingerprint.

#### Scenario: Two publishes race on one key
- **WHEN** two PublishRelease calls run at the same time with the same key, caller, namespace and body fingerprint
- **THEN** one release exists and both answers carry the first publisher subject string and time

### Requirement: Failed first publish retryable

When the first publish with an Idempotency-Key fails, the server SHALL store no replay answer for that key.

#### Scenario: First publish fails validation
- **WHEN** the first PublishRelease with an Idempotency-Key fails with code registry.invalid
- **THEN** the server stores no answer and the same key with a fixed request runs a new publish

### Requirement: Served flag in version list

The server SHALL include a served flag in each entry of the registry version list.

#### Scenario: List versions with removed signing key
- **WHEN** ListVersions lists a release whose signing key was removed
- **THEN** the entry marks served false and a served release marks served true

#### Scenario: List versions with no trusted key
- **WHEN** ListVersions lists a release with no trusted signing key
- **THEN** the entry marks served false

#### Scenario: List flag agrees with download
- **WHEN** ListVersions and the registry download path read the same release
- **THEN** an entry marked served false is not served for download and an entry marked served true is served

#### Scenario: List without read permission
- **WHEN** a caller without registry read access lists versions
- **THEN** the server refuses with a 403 denial
