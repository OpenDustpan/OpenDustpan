## ADDED Requirements

### Requirement: Focus for one unit

When a caller with read requests Focus for one unit of one unit, the server SHALL answer the focus view with folds and completeness metadata.

#### Scenario: Focus for one unit on one unit
- **WHEN** A caller with read requests Focus for one unit for one unit by tool and stable unit ID.
- **THEN** The server answers the focus unit, at most 12 neighbors each way by default and 50 at most, folds by collection and completeness with complete, truncated, hidden and since.

#### Scenario: Hidden units fold
- **WHEN** A Focus for one unit walk reaches a unit the caller cannot read.
- **THEN** The server skips hidden units in the walk and counts them in hidden.

#### Scenario: Window applies
- **WHEN** A caller requests Focus for one unit without a window.
- **THEN** The server counts only edges seen in the last 90 days by default.

#### Scenario: Edge cap truncates
- **WHEN** One hop of Focus for one unit reads more than 5000 edges.
- **THEN** The server sets truncated true and keeps at most 5000 edges read in one hop.

### Requirement: Enforce focus limits

When a focus request exceeds hops, node or edge limits, the server SHALL answer 400.

#### Scenario: Request above node limit
- **WHEN** A caller requests more than 500 units or hops outside 1 to 5 in one focus answer.
- **THEN** The server answers 400 and names the one command that helps.
