## ADDED Requirements

### Requirement: Read and commit methods

The server SHALL provide Head, ReadFile, Compare, Commit and FindCommit in the GitHub adapter behind their capabilities.

#### Scenario: Read file
- **WHEN** a caller reads a path at a ref
- **THEN** the adapter returns the file bytes

#### Scenario: Unsupported call
- **WHEN** the host cannot do a call
- **THEN** the adapter returns ErrUnsupported

### Requirement: Review methods

The server SHALL provide OpenReview, ReadReview and CloseReview in the GitHub adapter behind their capabilities.

#### Scenario: Close review
- **WHEN** a caller closes an open review
- **THEN** the adapter marks the review closed at the host
