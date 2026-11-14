## ADDED Requirements

### Requirement: Zero window asks each time

When the window is 0, the server SHALL ask for a fresh authentication at each guarded step.

#### Scenario: Zero window with old step
- **WHEN** The window is 0 and the step age is above 0
- **THEN** The server refuses the step with 403 mfa.fresh-step-required and changes nothing.

#### Scenario: Zero window with zero age
- **WHEN** The window is 0 and the step age is 0
- **THEN** The server refuses the step with 403 mfa.fresh-step-required and changes nothing.

### Requirement: Stale error names sign-in path

When a step lacks fresh authentication, the server SHALL answer 403 with code mfa.fresh-step-required and the path of a new sign-in.

#### Scenario: Stale session error text
- **WHEN** A step lacks fresh authentication
- **THEN** The answer holds code mfa.fresh-step-required and the path of a new sign-in.

### Requirement: No factor names enrollment

When the person has no enrolled factor, the server SHALL refuse the guarded step with 403 mfa.fresh-step-required and the enrollment page.

#### Scenario: No enrolled factor
- **WHEN** The person has no enrolled factor and asks for a guarded step
- **THEN** The server refuses with 403 mfa.fresh-step-required and names the enrollment page.

### Requirement: Token never satisfies

When an access token asks for a guarded step, the server SHALL refuse with 403 mfa.fresh-step-required.

#### Scenario: Access token use
- **WHEN** An access token asks for a guarded step
- **THEN** The server refuses with 403 mfa.fresh-step-required and changes nothing.

### Requirement: Shortest window holds

When a namespace or approval rule sets a shorter window, the server SHALL use the shortest window that holds for the step.

#### Scenario: Namespace sets 0 against 900
- **WHEN** The installation window is 900 and the namespace window is 0
- **THEN** The server asks for a fresh authentication at each step of that namespace.

#### Scenario: Approval rule sets 0
- **WHEN** The approval rule window is 0 and the installation window is 900
- **THEN** The server asks for a fresh authentication at each step of that rule.

### Requirement: Boundary stays fresh

When the window is above 0 and the step age equals the window, the server SHALL treat the step as fresh.

#### Scenario: Age equals nonzero window
- **WHEN** The window is above 0 and the step age equals the window
- **THEN** The server accepts the step as fresh.

### Requirement: Invalid window refused

When the window value is not 0 and not 60 to 900, the server SHALL refuse the setting.

#### Scenario: Window 30 refused
- **WHEN** An administrator sets freshAuthSeconds to 30
- **THEN** The server refuses the setting and keeps the prior window.

### Requirement: Window change needs fresh

When an administrator changes the window to a longer value, the server SHALL ask for a fresh authentication before the change.

#### Scenario: Change to longer needs fresh
- **WHEN** An administrator changes the window to a longer value
- **THEN** The server asks for a fresh authentication before the change.

#### Scenario: Change to 0 needs none
- **WHEN** An administrator changes the window to 0
- **THEN** The server accepts the change without a fresh authentication.
