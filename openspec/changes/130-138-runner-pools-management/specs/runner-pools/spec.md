## ADDED Requirements

### Requirement: List the runner pools from the CLI

When the caller runs `opendustpan runner pools list --namespace <ns>` with a token that holds namespace:read-runners, the CLI SHALL print one line for each pool of the namespace, sorted by name, and exit 0.

#### Scenario: List two pools
- **WHEN** the namespace holds the pools "jobs" and "sweeps" and the caller runs the command
- **THEN** the command prints two lines, "jobs" first, and exits 0

#### Scenario: Server refuses
- **WHEN** the server answers 403
- **THEN** the command prints the server message on stderr and exits 1
