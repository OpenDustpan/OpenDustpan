## ADDED Requirements

### Requirement: namespace-create-names-name

When the person runs namespace create with no name, the CLI SHALL answer that the namespace name is missing and show the create command with a name.

#### Scenario: create with no name
- **WHEN** the person runs namespace create with no name
- **THEN** the CLI answers that the namespace name is missing and shows the create command with a name

### Requirement: stack-list-without-console

When no console is configured, the server SHALL omit the console URL from the stack list.

#### Scenario: list stacks with no console
- **WHEN** no console is configured
- **THEN** the stack list shows no console URL

### Requirement: passkey-without-public-url

When a passkey route runs with no public URL, the server SHALL answer status 404 that names `--public-url`.

#### Scenario: passkey call with no public URL
- **WHEN** any caller calls a passkey route while the server has no public URL
- **THEN** the answer keeps status 404 and names `--public-url`

### Requirement: plan-retention-hint

When `--plan-retention` fails to parse at server start, the server SHALL fail start with an error that names the flag plus the default value and the duration format.

#### Scenario: empty plan retention
- **WHEN** the person starts the server with an empty `--plan-retention` value
- **THEN** the start fails with an error that names `--plan-retention` with its default value and duration format

#### Scenario: malformed plan retention
- **WHEN** the person starts the server with `--plan-retention` set to a text that is not a duration
- **THEN** the start fails with the same error and repeats nothing of the input

#### Scenario: negative plan retention
- **WHEN** the person starts the server with a negative `--plan-retention` value
- **THEN** the start fails with the same error
