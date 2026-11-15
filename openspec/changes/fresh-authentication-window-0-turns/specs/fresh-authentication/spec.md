## ADDED Requirements

### Requirement: Zero window asks each time

While the window is 0, the server SHALL ask for a fresh authentication at each guarded step.

#### Scenario: Zero window with age 0
- **WHEN** the window is 0 and the step age is 0
- **THEN** the server refuses with 403 mfa.fresh-step-required and changes nothing

### Requirement: Stale error path

When a step lacks fresh authentication, the server SHALL answer 403 with the code mfa.fresh-step-required and the path of a new sign-in.

#### Scenario: Stale session
- **WHEN** a valid session has a sign-in older than the window
- **THEN** the answer is 403 and holds the code and the path of a new sign-in

#### Scenario: Session that is not valid
- **WHEN** a session is missing or expired
- **THEN** the answer is 401, as before, and this change does not touch it
