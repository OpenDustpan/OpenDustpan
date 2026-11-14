## ADDED Requirements

### Requirement: Write audited namespace setting

When a namespace administrator writes sourcefilter or outputshare, the server SHALL call Write audited namespace setting to write the setting row and one audit entry in the same transaction.

#### Scenario: sourcefilter write via Write audited namespace setting
- **WHEN** namespace administrator writes the sourcefilter setting through Write audited namespace setting
- **THEN** setting row is stored and one audit entry is stored with bounded old value

#### Scenario: outputshare write via Write audited namespace setting
- **WHEN** namespace administrator writes the outputshare setting through Write audited namespace setting
- **THEN** setting row is stored and one audit entry is stored with bounded old value

#### Scenario: audit insert fails in Write audited namespace setting
- **WHEN** audit insert fails inside Write audited namespace setting
- **THEN** setting row is rolled back and no new setting value reads back

#### Scenario: concurrent write via Write audited namespace setting
- **WHEN** two administrators write the same setting at the same time
- **THEN** one write wins and the losing write reads back the winning value with its audit entry

#### Scenario: refused write to Write audited namespace setting
- **WHEN** caller without the namespace write grant writes sourcefilter or outputshare
- **THEN** server refuses with 403 and stores no setting row and no audit entry
