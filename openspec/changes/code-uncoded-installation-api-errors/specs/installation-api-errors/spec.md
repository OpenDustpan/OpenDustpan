## ADDED Requirements

### Requirement: Coded installation errors

When the installation API answers an error from a migrated classify function, the server SHALL include the catalog errorCode in the answer.

#### Scenario: Wrapped sentinel answers with code
- **WHEN** installation API handler returns a wrapped sentinel such as ErrNoPaging or ErrNoWorker
- **THEN** answer carries the catalog errorCode with existing status and message

#### Scenario: Overlapping match keeps chain order
- **WHEN** two classify functions match the same error
- **THEN** answer carries the code of the first classify function in the combined chain

#### Scenario: Uncoded list shrinks
- **WHEN** migrated path answers without a catalog code
- **THEN** server.txt holds no line for the migrated class and the errcode gate passes

### Requirement: Keep status and message

While migrating a classify function onto the Writer Classify hook, the server SHALL keep its status and message unchanged.

#### Scenario: Status and message stable
- **WHEN** migrated class answers an error
- **THEN** status and message match the values before the move

### Requirement: Coded arguments

When a migrated installation API error names a value in its text, the server SHALL carry each declared argument of the code in errorArgs.

#### Scenario: Arguments carried
- **WHEN** migrated class answers an error whose text names a value
- **THEN** answer carries each declared argument of the code in errorArgs

#### Scenario: Missing argument value
- **WHEN** declared argument has no value for the error
- **THEN** answer carries an empty string for that argument with the same code

### Requirement: Codes in API description

When a slice adds catalog codes, the server SHALL list each code with its arguments in the API description.

#### Scenario: Description lists codes
- **WHEN** a slice adds catalog codes
- **THEN** API description lists each new code with its argument kinds and the client exposes the codes

### Requirement: Codes frozen

When a slice adds catalog codes, the server SHALL enter each code with its arguments in the shipped frozen lists.

#### Scenario: Shipped list grows
- **WHEN** a slice adds catalog codes
- **THEN** shipped frozen list holds each new code with its argument names and kinds and no old meaning changes

### Requirement: Fallback stays coded

When no classify function maps an installation API error, the server SHALL answer a catalog code with the existing fallback status and message.

#### Scenario: Unmapped error answered
- **WHEN** no classify function in the combined chain maps the error
- **THEN** answer carries a catalog code with the existing fallback status and message
