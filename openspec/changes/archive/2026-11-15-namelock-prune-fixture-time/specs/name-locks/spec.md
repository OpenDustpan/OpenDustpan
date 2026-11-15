## ADDED Requirements

### Requirement: The prune case does not depend on elapsed time

The conformance case of the name lock prune SHALL place the time of each count that it expects to keep, so that its result does not depend on the time between its writes and the prune.

#### Scenario: A slow machine
- **WHEN** a second passes between the insert of the recent count and the prune
- **THEN** the prune deletes only the old count, and the case passes
