## ADDED Requirements

### Requirement: Replacement disjoint by closure

When plan A replaces instance X, the checker SHALL answer disjoint for plans A and B only when X is not in the read set or the write set of B, the downstream closure of X is in the write set of A, no address of B is in that closure or reads it, and both plans are provable.

#### Scenario: Closure stays in its own plan
- **WHEN** plan A replaces `aws_instance.web` and updates each instance that reads it, and plan B updates `aws_s3_bucket.logs`, which reads nothing in that closure
- **THEN** the checker answers disjoint in both directions

#### Scenario: Closure reaches the other plan
- **WHEN** plan B updates an instance that reads `aws_instance.web.private_ip`
- **THEN** the checker answers a conflict with the reason `closure-reaches-other-plan` and names both addresses

#### Scenario: Reader outside the write set
- **WHEN** an instance reads `aws_instance.web` and plan A does not change it
- **THEN** the checker answers a conflict with the reason `closure-not-in-write-set`

### Requirement: Unproven closure is a conflict

When the checker cannot prove an edge of the closure, the checker SHALL answer a conflict with the reason `closure-unproven`.

#### Scenario: Unresolved module output
- **WHEN** an instance reads a module output that the plan JSON does not resolve, and the output may read the replaced instance
- **THEN** the checker answers a conflict with the reason `closure-unproven`

#### Scenario: Provider configured from the replaced instance
- **WHEN** a provider configuration reads an attribute of the replaced instance
- **THEN** the checker answers a conflict

#### Scenario: Limit reached
- **WHEN** the closure walk reaches its node or edge limit
- **THEN** the checker answers a conflict with the reason `closure-unproven`

#### Scenario: Zero value
- **WHEN** a caller passes a plan closure with no graph
- **THEN** the checker answers a conflict

### Requirement: Other cases stay conflicts

The checker SHALL answer a conflict for a delete, a provider change, a rename through a `moved` block or an alias, a data source, a change of a parent resource, an unseen reader, an unknown operation kind and any error.

#### Scenario: Delete
- **WHEN** plan A deletes an instance that plan B does not touch
- **THEN** the checker answers a conflict

#### Scenario: Unknown kind
- **WHEN** a plan holds an operation kind that the checker does not know
- **THEN** the checker answers a conflict

### Requirement: Hostile plan JSON

The decoder SHALL refuse a plan JSON above 64 MiB, with an unknown `format_version`, or with an address outside the address grammar, and SHALL mark an unresolved expression so that the graph is not provable.

#### Scenario: Oversized plan
- **WHEN** a plan JSON of 65 MiB is decoded
- **THEN** the decoder returns an error and the register route answers 422 `claim.plan-invalid`

#### Scenario: Address with control characters
- **WHEN** an address holds a newline
- **THEN** the decoder returns an error

### Requirement: Register route uses the checker

When a runner job or the local wrapper registers an apply claim with a replacement, the server SHALL accept it only when the checker answers disjoint against each live apply claim, and SHALL answer 409 with the reason and the addresses otherwise.

#### Scenario: Disjoint replacement accepted
- **WHEN** claim A replaces `aws_instance.web` with its closure, and live claim B is disjoint by the closure test
- **THEN** the server stores claim A as accepted

#### Scenario: Checker error
- **WHEN** the checker returns an error
- **THEN** the server answers 409 `claim.closure-unproven` and stores no claim
