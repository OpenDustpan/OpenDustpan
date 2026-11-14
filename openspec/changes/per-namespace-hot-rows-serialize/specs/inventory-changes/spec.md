## ADDED Requirements

### Requirement: NextInventoryChangeSeq commit order

While unit writes commit in one namespace, the server SHALL order their change numbers by commit with no skipped committed change.

#### Scenario: NextInventoryChangeSeq poll reader misses nothing
- **WHEN** unit writes commit in one namespace
- **THEN** a reader that polls by last seen number sees each committed change once
