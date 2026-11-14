## ADDED Requirements

### Requirement: Namespaced stored subject

When a caller creates a principal of a namespace, the server SHALL store the subject namespace:<namespace ID>/<short-name> built by the NEW constructor NewNamespacedSubject.

#### Scenario: Same short name in two namespaces
- **WHEN** A namespace admin creates short name ci-deploy in acme and in beta.
- **THEN** Both creates succeed with distinct stored subjects.

#### Scenario: Duplicate
- **WHEN** The same short name is created twice in one namespace.
- **THEN** The server answers 409 and stores no second row.

#### Scenario: Constructor refuses bad name
- **WHEN** The NEW constructor NewNamespacedSubject gets a short name that holds : or /.
- **THEN** It returns an error and no value.

### Requirement: Short name shape

If the short name is not 1 to 128 characters that match the allowlist, then the server SHALL refuse the create with the status 400.

#### Scenario: Bad characters
- **WHEN** The short name holds : or /.
- **THEN** The server answers 400 and stores nothing.

#### Scenario: Total bound
- **WHEN** The stored subject is longer than 256 characters.
- **THEN** The server answers 400 and stores nothing.

### Requirement: Reserved prefix

When a caller writes a principal through an installation route, the server SHALL refuse through ValidateSubject a subject that starts with namespace: without regard to ASCII case.

#### Scenario: Case variant ValidateSubject
- **WHEN** ValidateSubject checks an installation create that uses NAMESPACE:acme/x.
- **THEN** The server answers 400.

### Requirement: Identity provider subjects

Where the subject comes from an identity provider, the server SHALL skip ValidateSubject for that subject.

#### Scenario: Provider subject skips check
- **WHEN** An identity provider subject arrives for a person.
- **THEN** ValidateSubject does not run on it and the sign-in proceeds.
