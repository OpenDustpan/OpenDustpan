## ADDED Requirements

### Requirement: maker-loss-denies-use

When a service token is used after its maker lost the last act-as path to its principal, the server SHALL deny the use with token.outside-reach.

#### Scenario: maker loses grant then use denied
- **WHEN** a service token is used after its maker lost the last act-as path to its principal
- **THEN** the server denies the use with token.outside-reach and writes an audit entry
