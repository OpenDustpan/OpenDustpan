## ADDED Requirements

### Requirement: One membership function

When the identity provider adds a member to a group, the server SHALL apply the change only through one domain function that records the source and refuses protected groups without trust or full sync and closed groups.

#### Scenario: Refused protected add
- **WHEN** The identity provider adds a member to a protected group that does not trust the identity provider while full sync is off, through source scim, claim, or ldap.
- **THEN** The server returns ErrIdpAddRefused, adds no membership, and writes one audit entry that names the refusing rule.

#### Scenario: Accepted trusted and full-sync adds
- **WHEN** The identity provider adds a member to a protected open group that trusts the identity provider or while full sync is on.
- **THEN** The server adds the membership with the source and provider or token ID and writes one audit entry that names the allowing trust or full sync.

#### Scenario: Closed wins
- **WHEN** The identity provider adds a member to a closed group that trusts the identity provider or while full sync is on.
- **THEN** The server returns ErrIdpAddRefused, adds no membership, and writes one audit entry that names the closed switch.

#### Scenario: Unmapped group value
- **WHEN** A group value maps to zero groups by the explicit single-mapping rule or to an unknown group ID.
- **THEN** The server changes no membership and writes no audit entry for that value.

#### Scenario: Repeat add and source change
- **WHEN** The one function adds a member that is already present, or a SCIM, claim, or ldap add meets a local membership.
- **THEN** The repeat changes no row and writes no audit entry, and the source change keeps the row with the new source and writes one audit entry.

### Requirement: One transaction with audit

When the one function checks and writes memberships, the server SHALL do the check and the membership plus audit writes in one transaction.

#### Scenario: Concurrent switch and trust change
- **WHEN** A group close, trust removal, or full-sync switch races an in-flight SCIM batch or claim.
- **THEN** The server applies the flags as of the write transaction snapshot for that request and audits the outcome.

#### Scenario: Audit failure
- **WHEN** The audit write fails.
- **THEN** The server fails the change, adds no membership, and the SCIM adapter answers 500 distinct from a refusal.

### Requirement: SCIM and claim mapping

If the one function refuses an add, then the server SHALL map a SCIM call to a SCIM error and a group claim to a sign-in without that membership.

#### Scenario: SCIM error
- **WHEN** The one function refuses a SCIM add.
- **THEN** The server answers a SCIM error and adds no membership.

#### Scenario: Claim skip
- **WHEN** The one function refuses a group claim at sign-in.
- **THEN** The server allows the sign-in, adds no membership, and writes an audit entry.
