## ADDED Requirements

### Requirement: The fresh step is opt-in

The installation SHALL have the setting `freshAuthEnabled`, a boolean with the effective default `false`. The settings API SHALL show its effective value and source. While it is off, the server SHALL ask a person for no fresh authentication at the steps of the one fresh-authentication window. While it is on, each of those steps SHALL ask for one, as before. If the server cannot read the setting, it SHALL ask for one.

#### Scenario: Default off
- **WHEN** an installation has no value for `freshAuthEnabled`
- **THEN** the settings API shows `false` with its source `default`

#### Scenario: Token approves a destroy with the setting off
- **WHEN** the setting is off and an access token that holds the grant approves a `destroy`
- **THEN** the server accepts the approval and writes the audit entry

#### Scenario: Token is refused with the setting on
- **WHEN** the setting is on and an access token approves a `destroy`
- **THEN** the answer is 403 `mfa.fresh-step-required` and nothing changes

#### Scenario: Setting cannot be read
- **WHEN** the setting store returns an error
- **THEN** the server asks for a fresh step

### Requirement: Turning the setting off is a weaker change

A change of `freshAuthEnabled` from on to off SHALL need a fresh step while it is on. A change from off to on SHALL need none. Each change SHALL write an audit entry with the old and the new value.

#### Scenario: On to off without a step
- **WHEN** the setting is on and a request with no fresh step turns it off
- **THEN** the answer is 403 `mfa.fresh-step-required` and the setting stays on

#### Scenario: Off to on
- **WHEN** the setting is off and a request with an access token turns it on
- **THEN** the setting is on and an audit entry holds both values

### Requirement: Stored rules show that they are not enforced

While the setting is off, an approval rule with `freshAuthSeconds` and a namespace with `freshAuthForApprovals` SHALL keep their stored values. The rule API and the namespace API SHALL show `"enforced": false` for each of them. The server SHALL write no silent change to a rule.

#### Scenario: Rule with a window, setting off
- **WHEN** an approval rule has `freshAuthSeconds` 300 and the setting is off
- **THEN** the rule API shows 300 with `"enforced": false`

### Requirement: Only a person approves a destroy without a step

With the setting off, the server SHALL accept an approval of a `destroy` with `auth: none` only from a principal of the kind person. The handler, the service and the store SHALL each allow that one kind and refuse every other kind. Each layer SHALL refuse alone, with no help from the other layers.

#### Scenario: A service principal cannot approve a destroy with the setting off
- **WHEN** the setting is off and a service principal that holds `job:approve` approves a `destroy`
- **THEN** the approval is refused and nothing changes

#### Scenario: A workload principal cannot approve a destroy with the setting off
- **WHEN** the setting is off and a workload principal that holds `job:approve` (a job token or a trust-rule token) approves a `destroy`
- **THEN** the approval is refused and nothing changes

#### Scenario: An agent principal cannot approve a destroy with the setting off
- **WHEN** the setting is off and an agent principal approves a `destroy`
- **THEN** the approval is refused and nothing changes

#### Scenario: A person with an access token approves a destroy with the setting off
- **WHEN** the setting is off and a person's access token that holds `job:approve` approves a `destroy`
- **THEN** the approval is accepted with `auth: none` and the audit entry is written

### Requirement: A confirmation recorded with no step stops counting when the setting is on

The confirmation gate SHALL read the setting when it evaluates the approvals. A confirmation that records `auth: none` SHALL count only while the setting is off. While the setting is on, it SHALL NOT count, also when the server recorded it with the setting off, and the gate SHALL need a valid fresh authentication.

#### Scenario: Setting turned on after a confirmation with no step
- **WHEN** a confirmation was recorded with `auth: none` while the setting was off, and the setting is now on
- **THEN** the confirmation gate stays waiting and the job does not start its phase

#### Scenario: Confirmation with no step and the setting off
- **WHEN** the setting is off and the starter's confirmation records `auth: none`
- **THEN** the confirmation gate opens

### Requirement: Steps that stay

The first-administrator code, the issue of an enrollment code and the proof of a break-glass pass SHALL NOT depend on the setting.

#### Scenario: First-administrator code with the setting off
- **WHEN** the setting is off and a person uses the first-administrator code
- **THEN** the server asks for the code as before

### Requirement: A factor change needs a fresh step whatever the setting says

The change or reset of a factor (a passkey begin, finish or removal, and the reset of a local user) SHALL need a fresh step whatever the setting says. The route handler SHALL check it, and `internal/mfasvc` SHALL check it again in each of its begin, finish and remove methods. Each check SHALL refuse alone, with no help from the other.

#### Scenario: Passkey removal with the setting off and no fresh step
- **WHEN** the setting is off and a caller with an access token or a stale session removes a passkey
- **THEN** the handler refuses with 403 `mfa.fresh-step-required`

#### Scenario: Service called with no fresh step
- **WHEN** a caller reaches the `internal/mfasvc` remove or register method with an `Actor` that has no fresh step, and the setting is off
- **THEN** the service refuses and writes a refusal audit entry

#### Scenario: Finish of a registration with no fresh step
- **WHEN** a caller reaches the `internal/mfasvc` finish-registration method with an `Actor` that has no fresh step, and the setting is off
- **THEN** the service refuses with `mfa.ErrFreshStep` and writes a refusal audit entry

#### Scenario: Local-user reset with the setting off and no fresh step
- **WHEN** the setting is off and a caller with no fresh step resets a local user
- **THEN** the server refuses with 403 `mfa.fresh-step-required`
