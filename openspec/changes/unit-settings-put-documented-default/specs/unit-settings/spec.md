## ADDED Requirements

### Requirement: Directory dot equals unset

When a unit settings PUT sets codeLocation.directory to ".", the server SHALL store it as unset.

#### Scenario: Copy effective value
- **WHEN** A PUT sets codeLocation.directory to ".".
- **THEN** The server stores the directory as unset by Replace, answers 200, and EffectiveDirectory later reads ".".

#### Scenario: Clear to unset
- **WHEN** A PUT sets directory to "." while the stored directory holds a real path.
- **THEN** The server stores the directory as unset by Replace and answers 200.

#### Scenario: Dot with other invalid field
- **WHEN** A PUT sets directory to "." and also sets another field to an invalid value.
- **THEN** The server answers 400 and stores nothing.

### Requirement: Null document refused

When a unit settings PUT sends the raw body null, the server SHALL refuse it with 400.

#### Scenario: Null body
- **WHEN** A PUT sends the raw body null.
- **THEN** The server answers 400 with the NEW error code `unit-settings.invalid` by Replace and stores nothing.

### Requirement: Repeated key refused

When a unit settings PUT names one object key two times with exactly matching bytes at any object level, the server SHALL refuse it with 400.

#### Scenario: Repeated top-level executor
- **WHEN** A PUT names the top-level executor key two times.
- **THEN** The server answers 400 with the NEW error code `unit-settings.invalid` by Replace and stores nothing.

#### Scenario: Repeated nested key
- **WHEN** A PUT names a key two times inside codeLocation or writeBack.
- **THEN** The server answers 400 and stores nothing.

#### Scenario: Case and escape variants
- **WHEN** A PUT sends keys that differ only by case or by Unicode escape.
- **THEN** The server treats them as different keys and applies normal validation.

#### Scenario: Error shape
- **WHEN** A PUT is refused for null or a repeated key.
- **THEN** The server answers 400 with the NEW error code `unit-settings.invalid` and the message repeats nothing of the input.

#### Scenario: Bulk put unchanged
- **WHEN** A bulk-put call repeats a key inside one document.
- **THEN** The server keeps its current behavior, because this change covers the single-unit PUT only.
