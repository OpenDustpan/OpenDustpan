## ADDED Requirements

### Requirement: List the runner pools from the CLI

When the caller runs `opendustpan runner pools list --namespace <ns>` with a token that holds namespace:read-runners, the CLI SHALL print one line for each pool of the namespace, sorted by name, and exit 0.

#### Scenario: List two pools
- **WHEN** the namespace holds the pools "jobs" and "sweeps" and the caller runs the command
- **THEN** the command prints two lines, "jobs" first, and exits 0

#### Scenario: Server refuses
- **WHEN** the server answers 403
- **THEN** the command prints the server message on stderr and exits 1

#### Scenario: Client-side refusal
- **WHEN** the caller omits `--namespace`, or the server answer has an unknown shape
- **THEN** the command prints a message on stderr that names the flag or the shape, prints no rows and exits 1
- **AND** the command writes no audit record, because the server audits what it refuses
