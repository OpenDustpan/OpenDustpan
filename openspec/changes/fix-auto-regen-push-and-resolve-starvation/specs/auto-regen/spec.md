## ADDED Requirements

### Requirement: The resolve run starts the push run

The dispatch job SHALL start each resolve run with a token of the regen GitHub App, so that the completion of a successful resolve run starts the push workflow.

#### Scenario: A successful resolve run is followed by a push run
- **WHEN** a resolve run for an open pull request ends with success
- **THEN** a run of the push workflow starts for that resolve run

#### Scenario: The push workflow keeps its guards
- **WHEN** the patch of a resolve run changes a path that is not generated
- **THEN** the push workflow refuses the patch and pushes nothing

### Requirement: A newer main does not cancel a resolve run

The resolve workflow SHALL group its runs by the pull request and its head, and SHALL NOT cancel a run in progress.

#### Scenario: Push to main during a resolve run
- **WHEN** main moves while a resolve run for a pull request head works
- **THEN** that resolve run continues to its end

#### Scenario: A second dispatch for the same head
- **WHEN** the find job selects a pull request head that already has a queued or running resolve run
- **THEN** the dispatch step starts no run and logs that a run for that head is already running

#### Scenario: A newer head of the pull request
- **WHEN** the pull request gets a new head while a resolve run for the old head works
- **THEN** the push workflow refuses the result of the old head and pushes nothing
