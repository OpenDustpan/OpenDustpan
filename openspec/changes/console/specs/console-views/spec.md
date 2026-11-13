## ADDED Requirements

### Requirement: Locked section reason

When a principal lacks the grant for a console section object, the server SHALL answer through the access endpoint with a generic reason.

#### Scenario: Open locked section
- **WHEN** a principal opens a console section without the grant for its object
- **THEN** the console shows a generic reason that names the grant to ask for and hides whether the object exists.

#### Scenario: Same answer for unseen and nonexistent
- **WHEN** a principal asks for the access of an unseen object and a nonexistent object
- **THEN** the server answers the same status, body and reason for both objects.
