## ADDED Requirements

### Requirement: Chat delivery scope

When the reference describes hostNeedsUpdate, the server SHALL state that chat URLs must pass send-time host validation and that a rejected URL gets no delivery.

#### Scenario: chat-host-refusal-gets-no-delivery
- **WHEN** a reader opens the hostNeedsUpdate field
- **THEN** the reference states hostNeedsUpdate does not guarantee chat delivery and a URL rejected by ChatHost gets no delivery
