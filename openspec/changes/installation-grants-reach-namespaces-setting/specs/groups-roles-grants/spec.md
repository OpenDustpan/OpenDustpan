## ADDED Requirements

### Requirement: The installation setting for the reach of grants on the installation

The server SHALL have the installation setting `access.installationGrantsReachNamespaces` with the default `true`. With `true`, a grant on `@server` reaches each namespace. With `false`, a grant on `@server` SHALL give only the actions of the operator scope and SHALL NOT give an action inside a namespace, in the decision of an action, in each list of objects and in the access review. A failed read of the setting SHALL deny the decision.

#### Scenario: Setting false denies a namespace action
- **WHEN** the setting is `false` and a caller that holds only a grant on `@server` of a role with a namespace action does that action in a namespace
- **THEN** the server answers 403 that names the action and the kind of the object ("on this namespace"), does not name the namespace, and changes nothing

#### Scenario: Operator action stays allowed
- **WHEN** the setting is `false` and the same caller does an action of the operator scope
- **THEN** the server allows the action

#### Scenario: Grant on the namespace still works
- **WHEN** the setting is `false` and a caller holds a grant on a namespace
- **THEN** the caller does the actions of its role in that namespace only

#### Scenario: Default reaches namespaces
- **WHEN** no one set the setting
- **THEN** the settings API shows the value `true` and a grant on `@server` reaches each namespace
