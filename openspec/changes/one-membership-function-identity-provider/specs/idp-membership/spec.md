## ADDED Requirements

### Requirement: One membership function

When the identity provider adds a member to a group, the server SHALL apply the change only through one domain function that records the source and refuses protected groups without trust or full sync and closed groups.

#### Scenario: Refused protected add
- **WHEN** The identity provider adds a member to a protected group that does not trust it while full sync is off.
- **THEN** The server returns ErrIdpAddRefused, adds no membership and writes one audit entry that names the refusing rule.

#### Scenario: Closed wins
- **WHEN** The identity provider adds a member to a closed group that trusts it or while full sync is on.
- **THEN** The server refuses the add, adds no membership and writes one audit entry that names the closed switch.

#### Scenario: Trusted add
- **WHEN** The identity provider adds a member to a protected open group that trusts it.
- **THEN** The server adds the membership with its source and writes one audit entry.

#### Scenario: Repeat add keeps source
- **WHEN** An add meets a local membership or a present membership of the same source.
- **THEN** The local source stays and one audit entry names both sources; a same-source repeat changes no row.

### Requirement: One transaction with audit

When the one function checks and writes memberships, the server SHALL do the check and the membership plus audit writes in one transaction.

#### Scenario: Audit failure on refused add
- **WHEN** The audit write for a refused SCIM add fails.
- **THEN** The server answers 500 and adds no membership.

#### Scenario: Audit failure on refused claim
- **WHEN** The audit write for a refused claim fails.
- **THEN** The server blocks the sign-in with 500 and adds no membership.

### Requirement: SCIM and claim mapping

If the one function refuses an add, then the server SHALL map a SCIM call to a SCIM error and a group claim to a sign-in without that membership.

#### Scenario: SCIM error
- **WHEN** The function refuses a SCIM add.
- **THEN** The server answers a SCIM error with code idp.protected-group that names the group and the rule.

#### Scenario: Atomic SCIM patch
- **WHEN** One row of a SCIM PATCH is refused and another is accepted.
- **THEN** The server refuses the whole request and commits no row.
