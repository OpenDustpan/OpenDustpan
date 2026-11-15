# name-locks Specification

## Purpose
A name lock holds a name for a short time, so that a sign-in cannot be tried without limit. This capability covers the conformance case that prunes expired counts of the name locks. It does not cover the lock limits or the lock store itself.

## Requirements

### Requirement: The prune case does not depend on elapsed time

The conformance case of the name lock prune SHALL place the time of each count that it expects to keep, so that its result does not depend on the time between its writes and the prune.

#### Scenario: A slow machine
- **WHEN** a second passes between the insert of the recent count and the prune
- **THEN** the prune deletes only the old count, and the case passes
