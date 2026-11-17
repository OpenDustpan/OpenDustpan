## ADDED Requirements

### Requirement: One principal for each housekeeping purpose

The server SHALL make one built-in service principal for each housekeeping purpose at the install, and SHALL write its grants as stored grants with the install as the creator and the install time.

#### Scenario: Listed after the install
- **WHEN** an operator lists the principals of a new installation
- **THEN** each housekeeping purpose has one principal of kind `service` and source `builtin`, and each has stored grants with a creator and a time

### Requirement: The principal list filters by source

The principal list SHALL keep only the principals of the source in the `source` query parameter, and SHALL count only those rows in its total and its cursor.

#### Scenario: Built-in principals
- **WHEN** an operator lists the principals with `kind=service&source=builtin`
- **THEN** the answer holds only the built-in principals, and the total counts only them

#### Scenario: Source that is not valid
- **WHEN** an operator lists the principals with a `source` that is not valid
- **THEN** the server answers 400
