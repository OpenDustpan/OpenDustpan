## ADDED Requirements

### Requirement: The OpenTofu CLI shows the refusal of a planner write

When a planner with a grant on a collection runs `tofu apply` or `tofu destroy` on a workspace in that collection, the server SHALL refuse the write with 403 and `access.denied`, and the CLI output SHALL show that refusal, not a not-found organization.

#### Scenario: tofu apply as planner
- **WHEN** a planner with a grant on a collection runs `tofu apply` on a workspace in it
- **THEN** the CLI exits non-zero, its output holds the server refusal, and its output does not say that the organization is not found

#### Scenario: tofu destroy as planner
- **WHEN** a planner with a grant on a collection runs `tofu destroy` on a workspace in it
- **THEN** the CLI exits non-zero, its output holds the server refusal, and its output does not say that the organization is not found
