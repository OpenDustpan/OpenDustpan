## ADDED Requirements

### Requirement: Registry

The server SHALL keep one registry keyed by provider kind plus adapter name and refuse a duplicate pair at start.

#### Scenario: Duplicate registration
- **WHEN** two adapters register the same kind and name
- **THEN** the server refuses to start and names the pair.

### Requirement: Unknown pair

When a block names an unknown kind plus name pair, the server SHALL refuse the save and the open with ErrInvalid.

#### Scenario: Unknown pair
- **WHEN** a block names a pair with no adapter
- **THEN** the save and the open fail with ErrInvalid without network use.

### Requirement: Conformance run

The server SHALL run the RunProvider and RunRotator suites for each adapter against a fake of its remote system.

#### Scenario: Fake remote errors
- **WHEN** a test adapter runs both suites
- **THEN** each case maps to the sentinel of the error table with no leaked goroutine.

### Requirement: Secret literal refusal

When a block gives a secret input as a plain literal, the server SHALL refuse the block with ErrInvalid.

#### Scenario: Plain secret literal
- **WHEN** a block gives a secret input as a plain literal
- **THEN** the save fails with ErrInvalid and no call reaches the network.

### Requirement: Error translation

When a provider call answers, the server SHALL translate the status to the sentinel of the error table and mask secret inputs in the stored message.

#### Scenario: Status table
- **WHEN** a call answers with a failure status
- **THEN** 400 maps to ErrInvalid, 401 and 403 to ErrRemoteDenied, 404 to ErrRemoteNotFound, and 429 and 5xx to ErrUnavailable.

#### Scenario: Job log mask
- **WHEN** an error message holds a secret input value
- **THEN** the job log holds no secret value.

### Requirement: Call limits

When a provider adapter calls its remote system, the server SHALL bound the request body to 256 KiB, the answer to 1 MiB, and the timeout to 10 seconds by default and at most 30 seconds.

#### Scenario: Oversize bodies
- **WHEN** a call sends or receives an oversize body
- **THEN** the call fails with ErrTooLarge for an answer above 1 MiB and ErrInvalid for a request above 256 KiB.

### Requirement: Guarded call

When a provider adapter calls its remote system, the server SHALL route the call through the shared outbound client with no redirect, HTTPS only, pinned dial guard and 1 MiB read limit.

#### Scenario: Blocked address
- **WHEN** a call targets a link-local, loopback or unlisted private address
- **THEN** the call fails with ErrUnavailable.

#### Scenario: Rebinding and redirect
- **WHEN** a call meets a rebinding answer or a cross-host redirect
- **THEN** the call fails with ErrUnavailable and sends no credential to another host.

### Requirement: Ordered blocks

When a profile open calls provider blocks, the server SHALL call the blocks in document order with at most 16 parallel calls to one host per server process.

#### Scenario: Full host slots
- **WHEN** more than 16 calls target one host
- **THEN** extra calls wait within the timeout and a wait that ends fails with ErrUnavailable.

### Requirement: Rotator and token source

When a Rotator or TokenSource call runs, the server SHALL apply the same error table, limits and guards as a Provider call.

#### Scenario: Rotator error map
- **WHEN** a Rotator call meets a refused, missing, timed-out or oversize answer
- **THEN** each failure maps to its sentinel with no partial credential stored.
