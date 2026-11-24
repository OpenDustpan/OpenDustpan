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

### Requirement: Steps that stay

The first-administrator code, the issue of an enrollment code and the proof of a break-glass pass SHALL NOT depend on the setting.

#### Scenario: First-administrator code with the setting off
- **WHEN** the setting is off and a person uses the first-administrator code
- **THEN** the server asks for the code as before

### Requirement: A factor change needs a fresh step whatever the setting says

The change or reset of a factor (a passkey begin, finish or removal, and the reset of a local user) SHALL need a fresh step whatever the setting says. The route handler SHALL check it, and `internal/mfasvc` SHALL check it again. Each check SHALL refuse alone, with no help from the other.

#### Scenario: Passkey removal with the setting off and no fresh step
- **WHEN** the setting is off and a caller with an access token or a stale session removes a passkey
- **THEN** the handler refuses with 403 `mfa.fresh-step-required`

#### Scenario: Service called with no fresh step
- **WHEN** a caller reaches the `internal/mfasvc` remove or register method with an `Actor` that has no fresh step, and the setting is off
- **THEN** the service refuses and writes a refusal audit entry

#### Scenario: Local-user reset with the setting off and no fresh step
- **WHEN** the setting is off and a caller with no fresh step resets a local user
- **THEN** the server refuses with 403 `mfa.fresh-step-required`
