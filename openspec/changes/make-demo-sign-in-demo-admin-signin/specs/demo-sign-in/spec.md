## ADDED Requirements

### Requirement: Seed demo-admin with sign-in

When the demo seed runs on a dev installation, the server SHALL create person demo-admin with the admin role on both demo namespaces and issue one single-use console sign-in plus one personal token for that person.

#### Scenario: Demo sign-in opens per-person pages
- **WHEN** the demo seed runs on a dev installation and the console signs in as demo-admin
- **THEN** GET /api/dustpan/v1/me/preferences, GET /api/server/caller/tokens, and session read answer for demo-admin without a person-missing refusal.

### Requirement: Refuse reuse and expiry

While the demo identity is in use, the server SHALL refuse a reused or expired one-time sign-in and an expired or revoked demo personal token.

#### Scenario: Reused sign-in is refused
- **WHEN** the one-time demo sign-in is used twice
- **THEN** The second use answers token.caller-not-person or token.invalid and creates no session.

#### Scenario: Expired token is refused
- **WHEN** the demo personal token is expired or revoked
- **THEN** The read answers token.ended or token.invalid.

#### Scenario: Bootstrap caller still refused
- **WHEN** a bootstrap-token caller reads a per-person route
- **THEN** The read keeps the existing person-missing refusal.
