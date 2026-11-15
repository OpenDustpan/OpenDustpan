## ADDED Requirements

### Requirement: An AI-authored pull request links a spec-ready change

CI SHALL fail an AI-authored pull request whose body names no spec-ready OpenSpec change, and SHALL NOT check a person's pull request.

#### Scenario: Agent pull request with a spec-ready link
- **WHEN** a pull request with a commit by `noreply@anthropic.com` names `openspec/changes/<id>` and that proposal says "Open questions: none"
- **THEN** the check passes

#### Scenario: Agent pull request with no link
- **WHEN** an AI-authored pull request names no change
- **THEN** the check fails and prints the line that names the fix

#### Scenario: Linked change is not spec-ready
- **WHEN** an AI-authored pull request names a change whose open questions are not "none"
- **THEN** the check fails and names that change

#### Scenario: Person's pull request
- **WHEN** a pull request has no AI signal
- **THEN** the check passes with no link
