## ADDED Requirements

### Requirement: delegation-record

The server SHALL store one delegation record for each delegation of a person to an agent principal, with the stable ID of the person, the stable ID of the agent, the mode, the expanded scope, the creator, the created time, the expiry, the client authentication and the audiences. The creator SHALL be the person.

#### Scenario: person creates a delegation to Dusty
- **WHEN** a person in a console session sends a create request for the agent principal Dusty with the mode `propose`, one namespace and an expiry within the effective maximum
- **THEN** the server stores one row with the stable IDs of the person, of the agent and of the namespace, the mode, the list of actions that the mode expanded to, the creator equal to the person, the created time, the expiry, the client authentication `server` and an empty list of audiences, and returns its ID

#### Scenario: scope is stored expanded
- **WHEN** a later change redefines what the mode `propose` expands to
- **THEN** a delegation that exists keeps its stored list of actions

#### Scenario: all namespaces does not cover the installation
- **WHEN** a person creates a delegation with all namespaces and `installation` false
- **THEN** the stored scope does not cover an action whose object is the installation or a principal

#### Scenario: only a person delegates
- **WHEN** a service principal, a workload or an agent sends a create request
- **THEN** the server refuses it and stores no row

### Requirement: delegation-needs-a-console-session

The server SHALL accept the create, renew and widen of a delegation only in a console session, and SHALL refuse an access token and a break-glass session.

#### Scenario: access token refused
- **WHEN** a caller sends a create request with an access token
- **THEN** the server answers 403 with the NEW error code `delegation.session-required` and stores no row

#### Scenario: break-glass session refused
- **WHEN** a person in a break-glass session sends a create, renew or widen request
- **THEN** the server answers 403 with the NEW error code `delegation.session-required` and changes no row

#### Scenario: agent refused
- **WHEN** an agent principal sends a create, renew, widen or revoke request
- **THEN** the server refuses it, changes no row and writes a denial entry

### Requirement: delegation-lifetime-is-a-setting

The server SHALL take the default and the maximum lifetime of a delegation from an installation setting, SHALL show the effective values in the API, and SHALL write an audit entry when the setting changes. For the client type `server` the shipped default is 30 days and the shipped maximum is 90 days.

#### Scenario: default lifetime
- **WHEN** a person creates a delegation to Dusty with no expiry
- **THEN** the server stores an expiry 30 days after the created time

#### Scenario: expiry above the maximum
- **WHEN** a person creates or renews a delegation with an expiry after the effective maximum
- **THEN** the server answers 422 with the NEW error code `delegation.invalid-expiry`, names the maximum, and changes no row

#### Scenario: expiry in the past
- **WHEN** a person creates a delegation with an expiry before the created time
- **THEN** the server answers 422 with the NEW error code `delegation.invalid-expiry` and stores no row

#### Scenario: effective values in the API
- **WHEN** a person reads the lifetime settings
- **THEN** the answer shows the default and the maximum of each client type and where each value comes from

### Requirement: delegation-renewal

The server SHALL renew a delegation under the same ID, with the same or a narrower scope, and SHALL write an audit entry for each renewal. The server SHALL NOT renew a delegation by itself.

#### Scenario: person renews
- **WHEN** a person in a console session renews a live delegation with an expiry within the effective maximum
- **THEN** the row keeps its ID, the expiry and the time of the last renewal change, and one audit entry names the renewal

#### Scenario: wider scope refused
- **WHEN** a person renews a delegation with a scope wider than the stored scope
- **THEN** the server refuses the request and changes no row

#### Scenario: no automatic renewal
- **WHEN** the expiry passes and nobody renews
- **THEN** the delegation ends with the cause `expired` and no renewal is made

### Requirement: delegation-ends-are-audited

The server SHALL end a delegation when the person revokes it, it expires, the agent is disabled or deleted, or the person is disabled, deleted, removed by SCIM or terminated by the identity provider. Each end SHALL write one audit entry with the cause.

#### Scenario: person revokes
- **WHEN** the person sends a revoke request for a live delegation
- **THEN** the row gets its end time and the cause `revoked`, and one audit entry names the delegation and the revoker

#### Scenario: expiry ends the delegation
- **WHEN** the expiry passes
- **THEN** `Resolve` refuses the delegation with the cause `expired` and one audit entry names the cause

#### Scenario: agent disabled or deleted
- **WHEN** an administrator disables or deletes the agent principal
- **THEN** each live delegation to it ends with the cause `agent-disabled` or `agent-deleted`, and each end writes one audit entry

#### Scenario: person leaves
- **WHEN** the person is disabled, deleted, removed by SCIM or terminated by the identity provider
- **THEN** each live delegation of the person ends with the matching cause, and each end writes one audit entry

#### Scenario: second revoke is idempotent
- **WHEN** an allowed caller revokes a delegation that is already ended
- **THEN** the server answers success, keeps the first cause and writes no second entry

### Requirement: delegation-list-and-read

The server SHALL return to a person the delegations of that person, and SHALL return every delegation to a caller with `server:manage-access` on the installation.

#### Scenario: person lists own delegations
- **WHEN** a person lists delegations
- **THEN** the server returns only the rows of that person, each with its live state, its expiry and the cause of its end where it ended

#### Scenario: empty list
- **WHEN** a person with no delegation lists delegations
- **THEN** the server answers 200 with an empty list

#### Scenario: row of another person hidden
- **WHEN** a person reads, renews or revokes the row of another person with no `server:manage-access`
- **THEN** the server answers 404 with the NEW error code `delegation.not-found` and changes no row

#### Scenario: administrator lists and revokes
- **WHEN** a caller with `server:manage-access` on the installation lists or revokes a delegation of another person
- **THEN** the server returns every delegation, or ends the row with the cause `revoked` and one audit entry that names the revoker

#### Scenario: unknown ID
- **WHEN** any caller reads or revokes an unknown ID
- **THEN** the server answers 404 with the NEW error code `delegation.not-found`

### Requirement: delegation-writes-need-the-audit-entry

The server SHALL write the audit entry of a create, a renew and an end in the same transaction as the write, and SHALL fail the write when the entry cannot be written.

#### Scenario: append fails
- **WHEN** the audit append of a create, a renew or an end fails
- **THEN** the row does not change and the server answers an error with no partial state
