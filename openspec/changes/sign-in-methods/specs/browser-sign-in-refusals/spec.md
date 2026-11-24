## ADDED Requirements

### Requirement: Replay refusal

When a browser sign-in reuses an OAuth state or a CAS ticket, the server SHALL refuse the sign-in with the generic browser answer, open no session, and write one audit entry.

#### Scenario: CAS ticket replay
- **WHEN** a CAS callback carries a ticket that a sign-in used before
- **THEN** the server gives 400 with the text "sign-in failed: start the sign-in again", opens no session, and writes one audit entry with the reason of the refusal.

#### Scenario: OAuth state replay
- **WHEN** an OAuth callback repeats a state that a sign-in used before
- **THEN** the server gives 400 with the text "sign-in failed: start the sign-in again" and opens no session.

### Requirement: Generic answer for a missing subject

When the answer of an identity provider has no subject, the server SHALL give the generic browser answer and SHALL write the code of the cause to the audit entry only.

#### Scenario: OAuth answer without sub
- **WHEN** the user-info answer of a plain OAuth provider has no `sub` claim
- **THEN** the server gives 400 with the text "sign-in failed: start the sign-in again", opens no session, and writes one audit entry with the reason `sign-in.subject-missing` (NEW).

#### Scenario: CAS answer without the subject attribute
- **WHEN** the validation answer of a CAS server has no value for the subject attribute of the provider
- **THEN** the server gives 400 with the text "sign-in failed: start the sign-in again" and opens no session.
