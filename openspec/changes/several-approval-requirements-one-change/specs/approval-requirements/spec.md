## ADDED Requirements

### Requirement: Several requirements on one job

When the approval defaults of the scopes of one unit give more than one requirement, the server SHALL start the job with one named gate for each requirement and SHALL open the approval wait only when every gate has its count of approvals. A requirement may name groups, users, unit owners, or no approvers.

#### Scenario: Two defaults give two gates
- **GIVEN** a namespace default of 2 approvals of group `platform` and a collection default of 1 approval with no approvers
- **WHEN** a person starts a job on a unit of that collection
- **THEN** the job waits in `awaiting-approval` with two gates named `default/<namespace scope key>` and `default`
- **AND** `GET .../jobs/{job_id}` lists each gate with its name, its count and its filled slots

#### Scenario: A default with an unless body fails the start
- **GIVEN** a second default that holds an `unless` body
- **WHEN** a person starts a job
- **THEN** the start fails with an error that wraps `gate.ErrUnavailable` and names the requirement
- **AND** the job is not stored

#### Scenario: More than 3 gates in a phase
- **WHEN** a decision gives more than 3 gates for one phase
- **THEN** the start fails with `ErrInvalid` and the job is not stored

### Requirement: Approvers are named by stable ID

The server SHALL name each user in the approvers of a gate by the stable principal ID, and SHALL keep the name of the user for display only. An entry with no stored ID SHALL count no one.

#### Scenario: A user is renamed
- **GIVEN** a gate that names the user Ann by principal ID
- **WHEN** an administrator renames Ann and Ann approves
- **THEN** the approval fills a slot of the gate

#### Scenario: A user is removed and made again
- **GIVEN** a gate that names the user Ann by principal ID, and Ann is removed and made again with the same name
- **WHEN** the new Ann approves
- **THEN** the approval fills no slot of the gate, because the ID differs

#### Scenario: An entry with no ID
- **GIVEN** a stored default whose user entry has no principal ID
- **WHEN** a person starts a job
- **THEN** the start fails with an error that wraps `gate.ErrUnavailable` and names the requirement

### Requirement: Unit owners resolve at each approval

When a gate names the unit owners, the server SHALL read the owners of the unit from the context at the scope of the rule or wider at each approval, and SHALL count only the entries `group:<name>` and `principal:<ID>`. The server SHALL fail closed on a read error.

#### Scenario: An owner approves
- **GIVEN** a gate of unit owners, and the context of the unit names `principal:<ID>` of Bo
- **WHEN** Bo approves
- **THEN** the approval is stored with `for` that holds the name of the gate

#### Scenario: An owner group approves
- **GIVEN** the context of the unit names `group:platform`, and Cy is an enabled member of that group
- **WHEN** Cy approves
- **THEN** the approval fills a slot of the gate

#### Scenario: A text owner
- **GIVEN** the context of the unit names an owner as plain text
- **WHEN** a person with that text as a name approves
- **THEN** the approval fills no slot of the gate

#### Scenario: The context cannot be read
- **WHEN** the read of the context fails at an approval
- **THEN** the server refuses the approval with 403, stores nothing, and writes an audit entry

#### Scenario: The owners change after an approval
- **GIVEN** Bo approved as an owner, and the context then names other owners
- **THEN** the approval of Bo still fills its slot

### Requirement: An approval counts as of the approval time

The server SHALL decide at the approval which gates the approver can fill, and SHALL NOT check the approver again when the job dispatches.

#### Scenario: An approver leaves the group
- **GIVEN** a person approved a gate of group `platform`
- **WHEN** the person leaves the group before the job dispatches
- **THEN** the approval still fills its slot and the job dispatches when every gate is filled

### Requirement: Each requirement keeps its own fresh window

When a requirement sets `freshAuthSeconds` and the installation setting `freshAuthEnabled` is on, the server SHALL let an approval fill a slot of that gate only when the fresh step of the approver meets the window of that gate. A gate with no window SHALL accept an approval with no fresh step.

#### Scenario: Two windows
- **GIVEN** gate A with `freshAuthSeconds` 300 and gate B with no window, and `freshAuthEnabled` on
- **WHEN** a person whose last fresh step is 10 minutes old approves
- **THEN** the approval is stored with `for` that holds gate B only

#### Scenario: A step that meets no window
- **GIVEN** every gate that holds the approver has a window that the fresh step does not meet
- **WHEN** the approver approves
- **THEN** the server refuses with 403 and the code `mfa.fresh-step-required`, and stores nothing

#### Scenario: The setting is off
- **GIVEN** `freshAuthEnabled` is off
- **THEN** the gates keep the stored `freshAuthSeconds` and the job response shows that it is not enforced

### Requirement: No unless on a job

The server SHALL refuse to start a job when an approval default that applies holds an `unless` body.

#### Scenario: A default with unless
- **WHEN** a person starts a job and a default of one scope holds an `unless` body
- **THEN** the start fails with an error that wraps `gate.ErrUnavailable` and says to remove the `unless` body

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

When a person approves, the server SHALL check the person against each gate of the phase by stable ID (group ID, principal ID, or the owners of the unit), SHALL record the names of the gates that hold the person on the approval, and SHALL refuse an approval when no gate holds the person.

#### Scenario: A member of one group
- **WHEN** a member of group A only approves a phase with gates A and B
- **THEN** the approval is stored with `for` that holds the name of gate A only

#### Scenario: A person in no group
- **WHEN** a person in no group of any gate approves
- **THEN** the server refuses with 403, the answer names the requirements of the phase, and no approval is stored
- **AND** an audit entry records the refusal

#### Scenario: Fail closed on a read error
- **WHEN** the group reader or the context reader returns an error, or the server has no reader
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

A rejection by any holder of `job:approve` SHALL reject the job, as before this change, whatever the approvers of the gates are.

#### Scenario: One rejection among several gates
- **GIVEN** a phase with two gates, and one gate has its approvals
- **WHEN** a holder of `job:approve` who is in no approver list of any gate rejects
- **THEN** the job is rejected
