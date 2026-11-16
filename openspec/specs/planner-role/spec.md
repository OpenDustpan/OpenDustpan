# planner-role Specification

## Purpose
The planner role covers what a planner can do and what the server refuses. A planner reads a collection and starts plans, and the server refuses each write of state. This capability states how the OpenTofu CLI shows that refusal.

## Requirements

### Requirement: The OpenTofu CLI shows the refusal of a planner write

When a planner with a grant on a collection runs `tofu apply` or `tofu destroy` on a workspace in that collection, the server SHALL refuse the write with 403 and `access.denied`, and the CLI output SHALL show that refusal, not a not-found organization.

#### Scenario: tofu apply as planner
- **WHEN** a planner with a grant on a collection runs `tofu apply` on a workspace in it
- **THEN** the CLI exits non-zero, its output holds the server refusal, and its output does not say that the organization is not found

#### Scenario: tofu destroy as planner
- **WHEN** a planner with a grant on a collection runs `tofu destroy` on a workspace in it
- **THEN** the CLI exits non-zero, its output holds the server refusal, and its output does not say that the organization is not found
