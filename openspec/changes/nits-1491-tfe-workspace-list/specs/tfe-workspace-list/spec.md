## ADDED Requirements

### Requirement: partial-read-lists-only-allowed-workspaces

When a principal lists workspaces, the server SHALL omit each workspace without unit:read.

#### Scenario: partial read lists only allowed workspaces
- **WHEN** a principal with unit:read on some workspaces lists workspaces through the TFE workspace list
- **THEN** the server returns only workspaces with unit:read and omits the rest
