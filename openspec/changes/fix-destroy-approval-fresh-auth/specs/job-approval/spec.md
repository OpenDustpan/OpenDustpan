## ADDED Requirements

### Requirement: The session gives the fresh step to the approval

With `freshAuthEnabled` on, the server SHALL read the fresh authentication of a person from the sealed session cookie of the request and pass it to the job service for a destroy approval and for a confirmation. A passkey or an MFA step SHALL count as an MFA step, and an SSO authentication as an SSO authentication. A password alone, an unknown kind, a missing time, an access token, an agent and a body field SHALL give no proof.

#### Scenario: Person with a fresh MFA step approves a destroy
- **WHEN** the setting is on and a person with a session that has an MFA step inside the window approves a `destroy`
- **THEN** the server accepts the approval and the audit entry names the kind and the time of the step

#### Scenario: Stale step
- **WHEN** the setting is on and the step of the session is older than the window
- **THEN** the server answers 403 with the sign-in path and records no approval

#### Scenario: SSO step for a destroy
- **WHEN** the setting is on and the session has an SSO step only
- **THEN** a destroy approval is refused as today, and a confirmation is accepted

#### Scenario: Forged proof
- **WHEN** a request body or a header carries a proof, or an access token approves a `destroy` with the setting on
- **THEN** the server ignores the body and the header and refuses the approval
