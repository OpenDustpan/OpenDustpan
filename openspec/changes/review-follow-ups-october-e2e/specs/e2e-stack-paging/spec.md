## ADDED Requirements

### Requirement: project-filter-paging

When pulumi stack ls runs without --all from a project directory, the server SHALL let the e2e assert project-filtered paging with no omission or duplication.

#### Scenario: ProjectFilterPaging
- **WHEN** pulumi stack ls runs from a project directory with cross-project stacks present and no create or delete during the run
- **THEN** the CLI lists the 5 current-project stacks exactly once across more than one page and no other-project stack
