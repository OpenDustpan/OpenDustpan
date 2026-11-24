## ADDED Requirements

### Requirement: The installation setting limits the reach of a grant on the installation

The installation SHALL have the setting `access.installationGrantsReachNamespaces`, a boolean with the effective default `true`. While the value is `true`, a grant on the installation object SHALL reach each namespace. While the value is `false`, a grant on the installation object SHALL give only the actions of the operator scope, and SHALL give no action on an object inside a namespace.

#### Scenario: Default reach
- **WHEN** no value is set and a person holds `site-administrator` on the installation
- **THEN** the person is allowed `unit:read` on a unit of any namespace

#### Scenario: A grant on the installation gives no data action with the setting false
- **WHEN** the setting is `false` and a person holds only `site-administrator` on the installation
- **THEN** `unit:read` on a unit of a namespace is refused with 403 that names the action and the object

#### Scenario: A grant on the installation gives no action on a namespace object with the setting false
- **WHEN** the setting is `false` and a person holds only a role on the installation with `namespace:manage-ip-allowlist`
- **THEN** the change of the allowlist of a namespace is refused with 403

#### Scenario: Operator actions stay
- **WHEN** the setting is `false` and a person holds only a role on the installation with an operator action
- **THEN** the operator action on the installation is allowed

#### Scenario: The list agrees with the check
- **WHEN** the setting is `false` and a person holds only `site-administrator` on the installation
- **THEN** the list of units of the person is empty, and `namespaces/-` lists no namespace

#### Scenario: The value changes at once
- **WHEN** an administrator changes the setting from `true` to `false`
- **THEN** the next check of the person refuses the namespace action, and no grant row changes

#### Scenario: Token routes of a namespace principal
- **WHEN** the setting is `false` and a person holds `principal:manage-tokens` only on the installation
- **THEN** the person is refused the tokens of a principal of a namespace, and is allowed the tokens of a principal with no namespace

### Requirement: The reach rule fails closed and leaves other grants alone

An object inside a namespace is a namespace, a collection, a unit, or a principal that belongs to a namespace. If the server cannot read the setting or cannot parse its value, it SHALL act as for `false`. A grant on a namespace, a collection or a unit SHALL work as before in both modes.

#### Scenario: The setting cannot be read
- **WHEN** the reader of the setting returns an error
- **THEN** the namespace action through the installation grant is refused

#### Scenario: A grant on a namespace stays
- **WHEN** the setting is `false` and a person holds a role with the data family on one namespace
- **THEN** `unit:read` is allowed in that namespace and refused in another

### Requirement: The access review shows the reach of a grant on the installation

The access review SHALL show, for a grant on the installation, whether it reaches namespaces under the effective value of the setting.

#### Scenario: Review with the setting false
- **WHEN** the setting is `false`
- **THEN** each grant on the installation in the access review has `reachesNamespaces` set to `false`

### Requirement: Housekeeping namespace purposes work with the setting false

With the setting `false`, the namespace purposes of the housekeeping principals SHALL run on a new namespace through their grants on that namespace. The revoke of such a grant SHALL deny `namespace:manage-agent-runs`, and `server:sweep-agent-reveals` SHALL stay allowed on the installation.

#### Scenario: Purposes with the setting false
- **WHEN** the setting is `false` and a namespace is new
- **THEN** the retention, saved view cleanup, issuer rotation, reaper and reconcile purposes run on it
