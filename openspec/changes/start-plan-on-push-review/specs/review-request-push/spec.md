## ADDED Requirements

### Requirement: Push starts plans

When a push reaches a review request, the server SHALL start a plan job for each matching unit at the head commit of the push.

#### Scenario: Push starts plan
- **WHEN** a push reaches a review request that targets the repository and branch of the code location of a unit
- **THEN** a plan job starts at the head commit with source kind forge

### Requirement: Paths filter

When a push touches no path of the filter of a trigger, the server SHALL start no plan for that trigger.

#### Scenario: Unrelated push
- **WHEN** a push touches no file under the paths of the trigger
- **THEN** no plan starts

### Requirement: Fork refusal

When a push comes from a fork, the server SHALL refuse the push and write an audit entry.

#### Scenario: Fork push
- **WHEN** a push comes from a fork
- **THEN** no plan starts and an audit entry records the push

### Requirement: Replacement

When a newer push reaches the same review request, the server SHALL replace the older plan of the same trigger.

#### Scenario: Newer push
- **WHEN** a newer push arrives before the older plan ends
- **THEN** the older plan is canceled and the newer plan proceeds

### Requirement: Default paths

When a trigger is saved with empty paths, the server SHALL store the cleaned working directory of the code location as the filter.

#### Scenario: Empty paths
- **WHEN** a trigger is saved with empty paths
- **THEN** the stored filter is the cleaned working directory

### Requirement: Paths validation

If NewTrigger gets an empty, absolute or escaping paths pattern, then the server SHALL refuse the save with an error that names the pattern.

#### Scenario: Bad pattern
- **WHEN** NewTrigger gets the pattern ../x or /x
- **THEN** the save is refused with a request error that names the pattern
