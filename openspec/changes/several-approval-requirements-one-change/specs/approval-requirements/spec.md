## ADDED Requirements

### Requirement: Several group requirements on one job

When the approval defaults of the scopes of one unit give more than one requirement, and each requirement names only groups or no approvers, the server SHALL start the job with one named gate for each requirement and SHALL open the approval wait only when every gate has its count of approvals.

#### Scenario: Two defaults give two gates
- **GIVEN** a namespace default of 2 approvals of group `platform` and a collection default of 1 approval with no approvers
- **WHEN** a person starts a job on a unit of that collection
- **THEN** the job waits in `awaiting-approval` with two gates named `default/<namespace scope key>` and `default`
- **AND** `GET .../jobs/{job_id}` lists each gate with its name, its count and its filled slots

#### Scenario: A default that names users, unit owners, a fresh window or an unless body still fails the start
- **GIVEN** a second default that names users, names unit owners, sets `freshAuthSeconds`, or holds an `unless` body
- **WHEN** a person starts a job
- **THEN** the start fails with an error that wraps `gate.ErrUnavailable` and names the requirement

#### Scenario: More than 3 gates in a phase
- **WHEN** a decision gives more than 3 gates for one phase
- **THEN** the start fails with `ErrInvalid` and the job is not stored

### Requirement: Distinct people across requirements

The server SHALL count each approver for at most one slot of one gate of a phase, and SHALL assign approvers to slots with a maximum matching, so that the order of the approvals never changes the result.

#### Scenario: One person holds two groups
- **GIVEN** gate A of group `platform` needs 1 and gate B of group `dba` needs 1
- **WHEN** only one person who is in both groups approves
- **THEN** the job keeps waiting, because that person fills one slot

#### Scenario: A greedy assignment would fail
- **GIVEN** Ann is in groups `platform` and `dba`, and Bo is in `platform` only
- **WHEN** Bo approves first and Ann approves second
- **THEN** the job opens, with Bo in gate A and Ann in gate B
- **AND** the same two approvals in the reverse order give the same result

#### Scenario: The same person approves twice
- **WHEN** one person approves a phase a second time
- **THEN** the server refuses with `job.already-approved` and the person fills one slot

### Requirement: An approver fills only the gates that hold the approver

When a person approves, the server SHALL check the membership of the person by group ID for each gate of the phase, SHALL record the names of the gates that hold the person on the approval, and SHALL refuse an approval when no gate holds the person.

#### Scenario: A member of one group
- **WHEN** a member of group A only approves a phase with gates A and B
- **THEN** the approval is stored with `for` that holds the name of gate A only

#### Scenario: A person in no group
- **WHEN** a person in no group of any gate approves
- **THEN** the server refuses with 403, the answer names the requirements of the phase, and no approval is stored
- **AND** an audit entry records the refusal

#### Scenario: Fail closed on a read error
- **WHEN** the group reader returns an error, or the server has no reader
- **THEN** the server refuses the approval with 403 and stores nothing

#### Scenario: An approval with no gate list
- **GIVEN** a stored approval whose `for` is empty
- **THEN** the approval fills no slot

### Requirement: The starter fills only the gates that allow the starter

The server SHALL let the person who started the job fill a gate only when that gate sets `AllowSelf`.

#### Scenario: The starter approves
- **WHEN** the starter is in the group of a gate that does not set `AllowSelf` and approves
- **THEN** the server refuses with `job.self-approval`

### Requirement: Each approval names its requirements in the audit log

The server SHALL write the audit entry of each approval with the names of the gates that the approval can fill.

#### Scenario: Approval entry
- **WHEN** an approval is recorded
- **THEN** the audit entry has the change `requirements` with the names of the gates, and the change `event`

### Requirement: Any rejection rejects the job

A rejection by a holder of `job:approve` SHALL reject the job, as before this change.

#### Scenario: One rejection among several gates
- **GIVEN** a phase with two gates, and one gate has its approvals
- **WHEN** a holder of `job:approve` rejects
- **THEN** the job is rejected
