## ADDED Requirements

### Requirement: The install guide is in the user documentation

The install page of the container image SHALL be `docs/guides/install.md`, linked from README.md, and SHALL say where the digest of each release is.

#### Scenario: Operator finds the page
- **WHEN** an operator opens README.md
- **THEN** a link leads to `docs/guides/install.md`

#### Scenario: Pull by digest
- **WHEN** an operator reads the section "Pull by digest"
- **THEN** the page shows the digest form of the reference, and names the summary of the release workflow run and `docker buildx imagetools inspect <image>:<tag>` as the places of the digest
