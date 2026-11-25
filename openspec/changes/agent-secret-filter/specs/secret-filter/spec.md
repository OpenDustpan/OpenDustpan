## ADDED Requirements

### Requirement: The secret filter is always on
`agent.SecretFilter` SHALL remove secret-like text from the prompt of a person, each tool result, each model answer and each failure text of a probe. No setting, flag or field SHALL turn it off.

#### Scenario: A canary in an answer
- **WHEN** a model answer holds the exact value of a secret item of the connection
- **THEN** the transcript holds `[removed: secret-like text]` and the run record holds the count of removals

#### Scenario: A canary in a tool call argument
- **WHEN** a tool call argument holds a secret-like text
- **THEN** the argument holds the marker where the text was

### Requirement: The filter keeps identifiers and hashes
The pattern list SHALL have no rule on entropy alone. The filter SHALL keep a resource ID, a content hash and a state version that appear in a corpus from state checks.

#### Scenario: A hash in an answer
- **WHEN** a model answer holds a SHA-256 hash of a blob
- **THEN** the text is unchanged and the count is 0

### Requirement: The filter never changes an opaque part
The filter SHALL NOT read or change an opaque reasoning part.

#### Scenario: A canary in an opaque part
- **WHEN** an opaque part holds the canary
- **THEN** the part is unchanged and no readable record holds the canary

### Requirement: The filter fails closed
A text above 4 MiB SHALL be replaced by the marker as a whole, with a count of 1. A scan SHALL take time linear in the text length.

#### Scenario: A very large text
- **WHEN** a model answer is above 4 MiB
- **THEN** the transcript holds the marker only

### Requirement: A document field never holds a secret value
The connect route SHALL refuse a document where a field holds the value of a secret item, and the error SHALL name the field.

#### Scenario: A value in a header field
- **WHEN** a header field of the document holds the exact value of a secret item
- **THEN** the route answers 400 and names the field
