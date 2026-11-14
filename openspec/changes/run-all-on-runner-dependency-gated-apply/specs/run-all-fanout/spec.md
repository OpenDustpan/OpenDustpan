## ADDED Requirements

### Requirement: Gate apply start on dependencies

When a change order holds Terragrunt dependency edges, the server SHALL start the apply job of a workspace only after each apply job it depends on succeeded.

#### Scenario: Ordered apply starts
- **WHEN** three workspaces form chain A then B then C and the apply jobs for A and B succeeded
- **THEN** the third apply job stays queued and makes no apply call until the second apply job succeeds, then it starts on its own workspace only.

### Requirement: Block on failed dependency

When a dependency apply job failed, the server SHALL block each dependent apply job and make no apply call for a blocked workspace.

#### Scenario: Failed middle unit blocks last unit
- **WHEN** three workspaces form chain A then B then C and the apply job for B failed
- **THEN** the third apply job is marked blocked with the blocking job named and makes no apply call.

### Requirement: Reject dependency cycle

When discovery writes Terragrunt edges with a cycle, the server SHALL reject the write and store no edges.

#### Scenario: Cycle at discovery write
- **WHEN** discovery writes edges with a cycle A then B then A
- **THEN** the server stores no edges and returns a cycle error naming the Terragrunt units in the cycle.

### Requirement: Limit dependency edges

When discovery writes more than 100 dependency edges for one change order, the server SHALL reject the write and store no edges.

#### Scenario: 101 edges
- **WHEN** discovery writes 101 edges for one change order
- **THEN** the server returns the error code change-order.too-many-edges, stores no edges and repeats nothing of the input.

### Requirement: Isolated retry

When a principal retries a failed run-all apply job, the server SHALL run the retry on the same workspace with the same idempotency key.

#### Scenario: Retry applies same workspace only
- **WHEN** an apply job failed and the same principal retries that apply job
- **THEN** the retry runs on the same workspace with the same Idempotency-Key, a duplicate retry returns the first result with no second apply, and each blocked dependent returns to queued after the retry succeeds.
