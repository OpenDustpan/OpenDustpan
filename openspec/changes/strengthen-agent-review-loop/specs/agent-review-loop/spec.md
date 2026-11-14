## ADDED Requirements

### Requirement: checklist-review

When a diff touches secrets, identity, routes, errors, migrations, limits, locks or crypto, the server SHALL require a checklist review before merge.

#### Scenario: Checklist applies
- **WHEN** a diff touches an identity route
- **THEN** the guide requires the checklist review before merge

#### Scenario: Failed checklist blocks merge
- **WHEN** the reviewer posts a blocking finding
- **THEN** the merge stays blocked until a person clears the finding

### Requirement: decision-marking

When the loop proposes a decision, the guide SHALL require an RFC or proposed decision marked agent-drafted.

#### Scenario: Agent proposal marked
- **WHEN** the loop proposes a decision
- **THEN** the document holds agent-drafted marking and waits for owner acceptance

### Requirement: triage-labels

When work needs a person or touches workflow files, the guide SHALL require the triage label ready-for-human or local-agent.

#### Scenario: Human work labelled
- **WHEN** work needs a person such as a release or a secret
- **THEN** the pull request carries ready-for-human and the loop does not close it

#### Scenario: Workflow work labelled
- **WHEN** an edit touches .github/workflows
- **THEN** the edit carries local-agent and the cloud loop skips it
