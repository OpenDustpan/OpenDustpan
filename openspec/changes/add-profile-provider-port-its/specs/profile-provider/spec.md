## ADDED Requirements

### Requirement: Provider port and registry

The server SHALL keep one registry keyed by provider kind plus adapter name and refuse a duplicate pair at start.

#### Scenario: Duplicate registration
- **WHEN** two adapters register the same kind and name
- **THEN** the server refuses to start and names the duplicate kind and name.

### Requirement: Unknown adapter

When a block names an unknown kind plus name pair, the server SHALL refuse the save and the open with ErrInvalid.

#### Scenario: Unknown pair
- **WHEN** a block names a kind plus name pair with no registered adapter
- **THEN** the save fails with ErrInvalid and the open fails with ErrInvalid without network use.

### Requirement: Conformance run

The server SHALL run the RunProvider and RunRotator suites for each adapter against a fake of its remote system.

#### Scenario: Fake remote errors
- **WHEN** a test adapter runs RunProvider and RunRotator against a fake remote
- **THEN** each case maps to the sentinel of the error table and the suite passes with no leaked goroutine.

### Requirement: Secret literal refusal

When a block gives a secret input as a plain literal, the server SHALL refuse the block with ErrInvalid.

#### Scenario: Plain secret literal
- **WHEN** a block gives a secret input as a plain literal
- **THEN** the save fails with ErrInvalid and no call reaches the network.

### Requirement: Error translation

When a provider call answers, the server SHALL translate the status to the sentinel of the error table and mask secret inputs in the stored message.

#### Scenario: Status table
- **WHEN** a provider call answers with a failure status
- **THEN** the open maps 400 to ErrInvalid, 401 and 403 to ErrRemoteDenied, 404 to ErrRemoteNotFound, and 429 and 5xx to ErrUnavailable.

#### Scenario: Redirect and message cap
- **WHEN** a provider call answers with a redirect or an error message
- **THEN** the open refuses the redirect with ErrUnavailable and keeps at most 200 characters of the message with each secret input masked.

#### Scenario: Job log mask
- **WHEN** a provider error message holds a secret input value
- **THEN** the job log holds no secret value and the stored message holds the masked form.

### Requirement: Call limits

When a provider adapter calls its remote system, the server SHALL bound the request body to 256 KiB, the answer to 1 MiB, and the timeout to 10 seconds by default and at most 30 seconds.

#### Scenario: Oversize bodies
- **WHEN** a provider call sends or receives an oversize body
- **THEN** the call fails with ErrTooLarge for an answer above 1 MiB and with ErrInvalid for a request above 256 KiB.

#### Scenario: Timeout
- **WHEN** a provider call exceeds the timeout of its block
- **THEN** the call fails with ErrUnavailable and writes no secret to the log.

### Requirement: Guarded call

When a provider adapter calls its remote system, the server SHALL route the call through the shared outbound client with no redirect, HTTPS only, pinned dial guard and 1 MiB read limit.

#### Scenario: Blocked address
- **WHEN** a provider call targets a link-local, loopback or unlisted private address
- **THEN** the call fails with ErrUnavailable and writes no secret to the log.

#### Scenario: Rebinding and redirect
- **WHEN** a provider call meets a rebinding DNS answer, a changed pinned IP or a cross-host redirect, including the URL of an external block
- **THEN** the call fails with ErrUnavailable without sending credentials to another host.

### Requirement: Ordered blocks

When a profile open calls provider blocks, the server SHALL call the blocks in order with at most 16 parallel calls to one host per server process.

#### Scenario: Parallel open
- **WHEN** an open calls several provider blocks
- **THEN** at most 16 calls run at the same time to one host per server process and ErrProvider maps first.

#### Scenario: Full host slots
- **WHEN** more than 16 calls target one host on one server process
- **THEN** extra calls wait for a free slot within the timeout of the block, and a wait that ends by timeout or cancel fails with ErrUnavailable.
