## ADDED Requirements

### Requirement: Registration version cases

When the guide describes PUT app-registration with If-Match, the server SHALL document 404 for an absent registration, 412 for a version mismatch, and success with the current version.

#### Scenario: absent-registration-gives-404
- **WHEN** a reader opens the app-registration section
- **THEN** the reference states PUT with If-Match gives 404 when absent, 412 on version mismatch, and success with the current version
