## ADDED Requirements

### Requirement: service-owned-filter-write

When a namespace administrator sets a namespace source filter, the server SHALL store the filter with the namespace fence and one audit record.

#### Scenario: Valid set persists
- **WHEN** a namespace administrator sets a valid filter
- **THEN** the server stores the filter with the namespace fence and one audit record

#### Scenario: Invalid set refused
- **WHEN** a namespace administrator sets a filter with an invalid pattern
- **THEN** the server returns source.filter-invalid and stores nothing

### Requirement: stored-filter-read

When a namespace administrator reads a namespace source filter, the server SHALL return the stored filter.

#### Scenario: Read after set
- **WHEN** a namespace administrator reads the filter after a valid set
- **THEN** the server returns the stored filter

#### Scenario: Unreadable store refused
- **WHEN** a namespace administrator reads the filter while the store cannot serve it
- **THEN** the server returns source.filter-unreadable
