## ADDED Requirements

### Requirement: Exclude refused namespaces

When a scope covers every object and a namespace allowlist refuses the client address, the server SHALL leave that namespace out of each list.

#### Scenario: Admin outside the allowlist
- **WHEN** an administrator lists namespaces from a refused address
- **THEN** the refused namespace is absent from the list

### Requirement: Show exclusions

When a scope covers every object with excluded namespaces, the server SHALL show the excluded namespaces next to all in caller access.

#### Scenario: Caller access
- **WHEN** the caller reads caller access from a refused address
- **THEN** the answer names the refused namespace
