## ADDED Requirements

### Requirement: One check of hidden characters

Every check of the server that refuses, changes or drops a hidden character (a control character, a format character, a line separator or a paragraph separator) SHALL use a function of `internal/ident`, and the golangci-lint configuration SHALL refuse a direct use of `unicode.IsControl` or of those category tables outside `internal/ident` and tests.

#### Scenario: A multi-line text with a right-to-left override
- **WHEN** a text that may hold line breaks holds U+202E
- **THEN** the check refuses it, and a line break alone passes

#### Scenario: An emoji password
- **WHEN** a password holds an emoji sequence with U+200D
- **THEN** the password check accepts it, as before the change
