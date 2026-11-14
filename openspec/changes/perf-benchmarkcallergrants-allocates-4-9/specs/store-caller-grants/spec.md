## ADDED Requirements

### Requirement: narrow-caller-grants-read

When the server reads caller grants for authorization, the server SHALL omit member source fields from the caller-grants query and exclude suspended members by filter.

#### Scenario: Hot path omits member source fields
- **WHEN** the server reads caller grants for authorization
- **THEN** the caller-grants rows carry no member source, member source id, or member suspended value

#### Scenario: Suspended member stays refused
- **WHEN** a group member is suspended
- **THEN** the suspended member gets no grant and the decision matches the wide read

### Requirement: wide-membership-view

When the server reads group membership views, the server SHALL return member source fields in the membership rows.

#### Scenario: Membership view keeps member source fields on both dialects
- **WHEN** the server reads group membership views
- **THEN** the view returns member source, member source id, and member suspended state on sqlite and postgres

### Requirement: dialect-parity

When the server reads caller grants, the server SHALL return the same grants on sqlite and postgres.

#### Scenario: Both dialects return equal grants
- **WHEN** the same members and grants exist in sqlite and postgres
- **THEN** the caller-grants read returns the same grants on both dialects
