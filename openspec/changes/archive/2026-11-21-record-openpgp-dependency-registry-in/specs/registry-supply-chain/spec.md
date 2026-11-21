## ADDED Requirements

### Requirement: registry-docs-signature-library

Where the registry docs describe signatures, the docs SHALL name the OpenPGP library that checks them and the command that shows its version, and SHALL NOT cite an ADR, an RFC or an issue.

#### Scenario: registry-docs-signature-library-present
- **WHEN** an Operator opens the registry docs section on signatures
- **THEN** the page names the library and the command that shows its version, and holds no ADR, RFC or issue reference.

### Requirement: adr-index-entry

Where the OpenPGP dependency ADR ships, the ADR index SHALL list its number and title.

#### Scenario: adr-index-entry-present
- **WHEN** an Operator opens the ADR index after the change
- **THEN** the index lists the number and title of the ADR.

### Requirement: adr-versions-match

Where the ADR records module versions, the versions SHALL equal the versions in go.mod.

#### Scenario: adr-versions-match-gomod
- **WHEN** the ADR versions equal go.mod
- **THEN** the version check passes.

#### Scenario: adr-versions-drift
- **WHEN** a dependency update makes a version differ from the ADR
- **THEN** the version check fails and names each module.

### Requirement: vuln-disposition

Where the finding GO-2026-5932 applies to x/crypto, the ADR SHALL record the fresh govulncheck output.

#### Scenario: govulncheck-matches
- **WHEN** the fresh output matches the issue report
- **THEN** the ADR records the affected symbol count.

#### Scenario: govulncheck-differs
- **WHEN** the fresh output differs from the issue report
- **THEN** the ADR records the fresh output and cites ADR 379 decision 2 for the risk.
