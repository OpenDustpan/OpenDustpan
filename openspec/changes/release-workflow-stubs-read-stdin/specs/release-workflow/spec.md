## ADDED Requirements

### Requirement: The release workflow tests give the exit code of the workflow

The tests of the release workflow steps SHALL use tool stubs that read their stdin as the real tools do, so that a step that pipes a token into a tool gives the exit code of the workflow and not 141.

#### Scenario: A token piped into the login of a stub
- **WHEN** the chart push step pipes a token of 100 KiB into the stubbed `helm registry login`
- **THEN** the step gives the exit code of its branch, never 141
