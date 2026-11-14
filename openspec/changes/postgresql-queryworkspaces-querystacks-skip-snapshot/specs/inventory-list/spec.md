## ADDED Requirements

### Requirement: SQLite single read transaction

When the server lists one sqlite inventory page, the server SHALL read the rows and the label rows in one read transaction.

#### Scenario: SQLite unit list stays consistent
- **WHEN** the server lists one sqlite inventory page while a label write runs between the statements
- **THEN** the row select and the label select belong to the same read transaction
