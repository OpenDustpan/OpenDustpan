## ADDED Requirements

### Requirement: precedent-table

The server docs SHALL provide a research note that records time-axis direction precedent for Arabic and Hebrew.

#### Scenario: Precedent table with dated sources
- **WHEN** a reader opens the research note
- **THEN** the note lists Material, Fluent, Grafana, one Arabic financial site, and one Hebrew financial site with access date and product version, axis direction, digit form, mixed-content behavior, and source link for each.

### Requirement: recommendation-with-disagreement-rule

When precedent is collected, the server docs SHALL state one time-axis recommendation with the disagreement rule applied.

#### Scenario: Disagreement still yields one recommendation
- **WHEN** collected precedent disagrees
- **THEN** the note states one direction recommendation under the disagreement rule and names which ADR holds the future rule plus the follow-up change name.

### Requirement: user-expectation-evidence

When the note answers user expectation, the server docs SHALL cite observed evidence separate from product precedent.

#### Scenario: Expectation cites observed evidence
- **WHEN** a reader asks what users expect
- **THEN** the note answers whether users expect time to run right to left or left to right with a cited study, survey, or native-speaker review separate from product precedent.
