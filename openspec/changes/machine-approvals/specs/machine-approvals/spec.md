## ADDED Requirements

### Requirement: A rule names a machine approver by stable ID

The server SHALL let a service principal, or the access token of a person, approve a job approval only when the approval rule names the principal by its stable ID in `machineIds`. A group, the entries of `users` and `unitOwners`, and the action `job:approve` alone SHALL NOT admit a non-person. The server SHALL refuse at save an entry that is not the ID of a stored principal of kind `service` or `person`.

#### Scenario: A named service principal approves
- **GIVEN** a rule that names the service principal `prn-bot` in `machineIds`, and a job that waits for one approval
- **WHEN** the service principal approves with its access token
- **THEN** the approval fills one slot and the job opens

#### Scenario: A service principal that no rule names
- **GIVEN** a service principal that holds `job:approve` and is in no `machineIds`
- **WHEN** it approves
- **THEN** the server refuses with 403, stores nothing and writes an audit entry of the outcome `denied`

#### Scenario: A group never admits a machine
- **GIVEN** a rule that names a group, and a service principal that is a member of the group
- **WHEN** the service principal approves
- **THEN** the server refuses with 403

#### Scenario: A person in users approves with an access token
- **GIVEN** a rule that names a person in `users` and not in `machineIds`
- **WHEN** the person approves with an access token
- **THEN** the server refuses with 403, and the same person approves with a console session

#### Scenario: A name instead of an ID
- **WHEN** an administrator saves a rule with a principal name in `machineIds`
- **THEN** the save fails with `gate.ErrInvalid`

#### Scenario: A principal is removed and made again
- **GIVEN** a rule that names `prn-bot`, and the service principal is removed and made again with the same name
- **WHEN** the new principal approves
- **THEN** the approval fills no slot, because the ID differs

### Requirement: A machine never approves its own job

The server SHALL refuse the approval of a machine for a job that the same principal started, and for a plan that the same principal authored, whatever `AllowSelf` and `authorMayApprove` say.

#### Scenario: The starter is a named machine
- **GIVEN** a gate with `AllowSelf` true, and a named service principal that started the job
- **WHEN** the service principal approves
- **THEN** the server refuses and the approval fills no slot

### Requirement: A machine fills no requirement that sets a fresh window

The server SHALL let a machine approval fill only the gates that store no fresh window, and SHALL refuse the approval when no such gate holds the principal.

#### Scenario: Two gates, one with a window
- **GIVEN** gate A with `freshAuthSeconds` 300 and gate B with no window, and a named machine
- **WHEN** the machine approves
- **THEN** the approval is stored with `for` that holds gate B only

#### Scenario: Only a gate with a window
- **GIVEN** every gate that holds the machine stores a window
- **WHEN** the machine approves
- **THEN** the server refuses with 403 and the code `mfa.fresh-step-required`

#### Scenario: A destroy with the setting off
- **GIVEN** `freshAuthEnabled` is off, a `destroy` approval with no stored window, and a named machine
- **WHEN** the machine approves
- **THEN** the approval is accepted and marked as a machine approval

#### Scenario: A destroy with the setting on
- **GIVEN** `freshAuthEnabled` is on and a named machine
- **WHEN** the machine approves a `destroy`
- **THEN** the server refuses with `mfa.fresh-step-required`

### Requirement: A machine approval is marked

The server SHALL mark each machine approval in the audit entry, in the stored approval and in the decision record, and SHALL set the marks itself.

#### Scenario: The audit entry
- **WHEN** a named machine approves
- **THEN** the audit entry has the change `machine` with the value `true`, the change `credential`, and the token ID

#### Scenario: A client sets the mark
- **WHEN** a client sends `machine: false` in the body of an approval
- **THEN** the server ignores the value and stores `machine` true

### Requirement: The kinds that stay refused

The server SHALL refuse the approval of a job by a job token, by a trust token of a workload and by an agent, with an audit entry, and SHALL keep ADR 225 decision 7 for a change order and a profile draft.

#### Scenario: A job token
- **WHEN** a job token approves a job
- **THEN** the server refuses with 403 and writes an audit entry

#### Scenario: An agent with a named token
- **WHEN** an agent acts with the access token of a named person
- **THEN** the server refuses with `ErrAgentDenied`

#### Scenario: A change order
- **WHEN** a service principal approves a change order
- **THEN** the server refuses
