## ADDED Requirements

### Requirement: Poll for work

When a runner with a valid runner key polls, the server SHALL return one claimed assignment or an empty poll answer through the NEW method `ClaimWork`.

#### Scenario: Work waits
- **WHEN** Runner with a valid runner key polls the NEW route `POST /runner/v1/claims` and work waits.
- **THEN** Server returns one assignment with the claim id and the 60 s expiry.

#### Scenario: Two runners race
- **WHEN** Two runners poll at once for one work item.
- **THEN** Server gives the work to one runner and returns an empty answer to the other.

#### Scenario: Bad key
- **WHEN** Runner polls with an unknown runner key.
- **THEN** Server denies with the NEW error code `runner.invalid` and returns no work.

#### Scenario: Retired runner
- **WHEN** Retired runner polls with its old runner key.
- **THEN** Server denies with `runner.retired` and returns no work.

#### Scenario: No work
- **WHEN** Runner polls and no work waits.
- **THEN** Server returns an empty poll answer with a retry delay.

### Requirement: Renew the lease

When the holder sends a heartbeat before expiry, the server SHALL renew the claim lease by 60 s through the NEW method `HeartbeatClaim`.

#### Scenario: Holder renews
- **WHEN** Holder sends a heartbeat to the NEW route `POST /runner/v1/claims/heartbeat` before expiry.
- **THEN** Server moves the expiry 60 s forward and returns the new expiry.

#### Scenario: Past holder renews
- **WHEN** A past holder sends a heartbeat after expiry.
- **THEN** Server denies with `runner.not-found` and keeps the current holder.

#### Scenario: Lost lease stops work
- **WHEN** Holder sends a heartbeat after another runner claimed the expired work.
- **THEN** Server denies with `runner.not-found` and the old holder stops its steps.

### Requirement: Heartbeat cadence

While a claim is held, the server SHALL accept heartbeats at least every 15 s with a 60 s expiry.

#### Scenario: Client cadence
- **WHEN** Runner heartbeats every 15 s with a 50 s long-poll timeout.
- **THEN** Server accepts the heartbeat and returns the new expiry.

### Requirement: Fence claim writes

When a claim write carries a stale fence, the server SHALL deny the write.

#### Scenario: Fence moved
- **WHEN** A claim write carries a stale fence.
- **THEN** Server denies the write and keeps the stored claim state.

### Requirement: Bounded report upload

When a runner streams a report under its claim, the server SHALL enforce 1 GiB total, 5000000 records, and 64 KiB per line through the NEW method `UploadChunk`.

#### Scenario: Line over bound
- **WHEN** Runner streams a line over 64 KiB under its claim to the NEW route `POST /runner/v1/uploads`.
- **THEN** Server denies with `runner.limit` and keeps the staged bytes.

#### Scenario: Report over bound
- **WHEN** Runner streams more than 1 GiB or more than 5000000 records under its claim.
- **THEN** Server denies with `runner.limit` and keeps staged bytes up to 1 GiB per claim.

#### Scenario: Resume after break
- **WHEN** Upload breaks and the holder resumes while the lease holds.
- **THEN** Server returns the staged byte offset and accepts only bytes from that offset.

### Requirement: Finish or fail a claim

When the holder completes or fails the claim before expiry, the server SHALL move the claim to done or failed through the NEW methods `CompleteClaim` and `FailClaim`.

#### Scenario: Complete
- **WHEN** Holder completes the claim before expiry.
- **THEN** Server moves the claim to done and keeps the encrypted staged report.

#### Scenario: Fail
- **WHEN** Holder fails the claim before expiry.
- **THEN** Server moves the claim to failed and deletes staged bytes.

#### Scenario: Expiry aborts upload
- **WHEN** The 60 s lease expires without a heartbeat.
- **THEN** Server returns the work to queued and deletes the staged bytes.
