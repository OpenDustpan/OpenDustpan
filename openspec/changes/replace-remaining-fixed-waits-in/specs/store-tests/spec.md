## ADDED Requirements

### Requirement: poll-waits

When a store test waits for a state change, the test helper SHALL poll the state with the NEW helper Poll until the shared per-test deadline expires.

#### Scenario: loaded-suite-passes
- **WHEN** the store suite runs under load beside another package test run
- **THEN** TestPollsPassUnderLoad passes on repeated loaded runs where a fixed 10 second wait failed.

### Requirement: timeout-error

When a poll reaches the shared per-test deadline, the helper SHALL fail the test with an error that names the wait and the awaited state.

#### Scenario: timeout-error-names-wait
- **WHEN** a poll reaches the shared per-test deadline without the state change
- **THEN** TestPollTimeoutNamesWaitAndState fails with an error that names the wait and the awaited state.

### Requirement: transient-retry

When a poll check returns a transient not-ready result, the helper SHALL retry the check until the shared per-test deadline expires.

#### Scenario: transient-retry
- **WHEN** a poll check returns a transient not-ready result
- **THEN** TestPollRetriesTransientNotReady keeps polling until the state appears.

#### Scenario: permanent-fails-fast
- **WHEN** a poll check returns a permanent error such as a closed database
- **THEN** TestPollFailsFastOnClosedDatabase fails at once with the check error.

### Requirement: cancel-stop

When the test is canceled during a poll, the helper SHALL stop polling without waiting for the deadline.

#### Scenario: cancel-stops-poll
- **WHEN** the test is canceled during a poll
- **THEN** TestPollStopsAtCancellation stops Poll without waiting for the deadline.
