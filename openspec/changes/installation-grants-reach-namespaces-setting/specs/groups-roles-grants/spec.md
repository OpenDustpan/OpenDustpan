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

#### Scenario: Setting false refuses the principal routes for a principal of a namespace
- **WHEN** the setting is `false` and a caller that holds only a grant on `@server` reads, changes, deletes, enables or disables a service principal of a namespace, or lists principals
- **THEN** the server answers 403 that names the action and "on this namespace" for the principal of a namespace, writes one audit entry with the outcome denied, changes nothing, and the list shows only principals of the installation

#### Scenario: Setting false leaves @server out of the token managers of a namespace principal
- **WHEN** the setting is `false` and the export of the grants that reach a service principal of a namespace is read
- **THEN** the export does not name a holder of a grant on `@server` as a manager of its tokens

#### Scenario: Default reaches namespaces
- **WHEN** no one set the setting
- **THEN** the settings API shows the value `true` and a grant on `@server` reaches each namespace

#### Scenario: Two authorizations in one request make one settings read
- **WHEN** one request authorizes two or more actions, or authorizes an action and lists objects, with the setting `false`
- **THEN** the server reads the setting one time for that request, and every decision of the request uses that one value

#### Scenario: A change before the next request is seen
- **WHEN** an operator changes the setting after a request has read it, and a second request then starts
- **THEN** the second request reads the new value, and the server keeps no value across requests
