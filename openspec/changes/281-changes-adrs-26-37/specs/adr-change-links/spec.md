## ADDED Requirements

### Requirement: Link lines

The docs SHALL list the ADR 281 change link in each of ADRs 26, 37, 43 and 60 and in ADR 281.

#### Scenario: reader-follows-current-rule
- **WHEN** a reader opens one of ADRs 26, 37, 43 or 60 and follows its Changed-by line
- **THEN** each of ADRs 26, 37, 43 and 60 lists a Changed-by line with verb Changes that points to ADR 281, ADR 281 lists the matching Changes lines, and the generated index current rule for these ADRs names ADR 281
