## ADDED Requirements

### Requirement: Cross-tool job read with rule

When a job token of a consumer named by a sharing rule of the producer requests outputs, the server SHALL return the root outputs of the latest state of the producer.

#### Scenario: Pulumi job reads OpenTofu outputs
- **WHEN** a job token of a Pulumi consumer named by a producer rule requests the outputs
- **THEN** the server returns the root outputs of the latest state of the producer and records an audit entry for the read.

#### Scenario: OpenTofu job reads Pulumi outputs
- **WHEN** a job token of an OpenTofu consumer named by a producer rule requests the outputs
- **THEN** the server returns the root outputs of the latest state and records an audit entry for the read.

#### Scenario: Producer with no state
- **WHEN** a named consumer job requests outputs and the producer has no latest state
- **THEN** the server returns 404 with error code unit.not-found and records an audit entry.

#### Scenario: Self read needs no rule
- **WHEN** a job token requests outputs of its own consumer unit
- **THEN** the server returns its own root outputs without a sharing rule.

#### Scenario: Empty collection matches nothing
- **WHEN** the only naming rule names a collection with no member units
- **THEN** the server denies the read with 403, error code unit.forbidden, and a denial audit entry.

### Requirement: Deny read with no rule

When no sharing rule of the producer names the consumer unit, collection, or namespace, the server SHALL deny the outputs read with 403.

#### Scenario: Read with no rule
- **WHEN** a job token requests outputs of a unit that no sharing rule names for its consumer
- **THEN** the server denies the read with 403, error code unit.forbidden, and a denial audit entry.

### Requirement: Secret output needs starter secrets action

When a named consumer job requests outputs that hold a secret output, the server SHALL require unit:read-secrets of the starter on the producer in addition to unit:read-outputs.

#### Scenario: Secret output without starter grant
- **WHEN** a named consumer job requests outputs that hold a secret output and the starter lacks unit:read-secrets on the producer
- **THEN** the server denies the read with 403, error code unit.forbidden naming unit:read-secrets, and a denial audit entry.

#### Scenario: Secret output with starter grant
- **WHEN** a named consumer job requests outputs that hold a secret output and the starter holds unit:read-secrets on the producer
- **THEN** the server returns secret outputs as ciphertext of the producer for the decrypt-only path.

### Requirement: Decrypt only outputs

When a job token that passes both read checks asks to decrypt producer ciphertext, the server SHALL decrypt only ciphertext that the root outputs of the latest checkpoint hold.

#### Scenario: Decrypt latest outputs ciphertext
- **WHEN** a passing job token asks to decrypt ciphertext that the root outputs of the latest checkpoint of the producer hold
- **THEN** the server decrypts it for the passing job token.

#### Scenario: Decrypt other ciphertext refused
- **WHEN** a passing job token asks to decrypt ciphertext from outside those root outputs
- **THEN** the server denies the decrypt with 403, error code unit.forbidden, and a denial audit entry.

#### Scenario: Client-encrypted latest state
- **WHEN** the States port reports the latest state as client-encrypted
- **THEN** the server returns the outputs sealed and decrypts only through the decrypt-only path after both checks pass.

#### Scenario: Unencrypted latest state
- **WHEN** the States port reports the latest state as not client-encrypted
- **THEN** the server returns plain outputs after both checks pass with no decrypt call.

### Requirement: Recheck at each read

When a job reads outputs of another unit, the server SHALL recheck the naming rule and the current starter grants at each read in the same store read that serves the response.

#### Scenario: Rule removed mid-job
- **WHEN** the producer rule is deleted after the job starts and before the job reads
- **THEN** the server denies the next read with 403, error code unit.forbidden, and a denial audit entry.

#### Scenario: Starter grant revoked mid-job
- **WHEN** the starter loses unit:read-outputs after the job starts and before the job reads
- **THEN** the server denies the next read with 403, error code unit.forbidden, and a denial audit entry.

#### Scenario: Change lands mid-flight
- **WHEN** a rule delete or grant revoke lands between the check and the response or decrypt of one read
- **THEN** the server denies the in-flight read with 403 and a denial audit entry.

### Requirement: Reject hostile read kind

When an outputs read names an object kind outside the allowlist, the server SHALL reject the request.

#### Scenario: Principal name as consumer
- **WHEN** an outputs read presents a principal name where a unit, collection, or namespace is required
- **THEN** the server rejects the request with 403, error code unit.forbidden, and a denial audit entry.

### Requirement: Output routes covered

When a caller reads outputs through a covered outputs route, the server SHALL enforce unit:read-outputs on the producer object and the naming rule of decision 5.

#### Scenario: Tool-neutral outputs route
- **WHEN** a job token calls GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs
- **THEN** the server checks unit:read-outputs on the producer and the naming rule.

#### Scenario: TFE and remote-state outputs routes
- **WHEN** a job token reads through current-state-version-outputs, state-version-outputs/{id}, or terraform_remote_state
- **THEN** the server checks the same action and rule before it answers.

#### Scenario: Undriven CLI version
- **WHEN** a caller reads through a route with a CLI version that no e2e step drives
- **THEN** the server returns the version error that names the supported e2e-driven versions.

### Requirement: Stable producer and consumer identity

When a unit is renamed or made again, the server SHALL match sharing rules by stable ID and never by name.

#### Scenario: Producer renamed
- **WHEN** the producer unit is renamed and a named consumer job reads
- **THEN** the server still matches the rule and returns the outputs.

#### Scenario: Producer removed and made again
- **WHEN** the producer unit is removed and made again under the same name
- **THEN** the server denies the read with 403, error code unit.forbidden, until a new rule names the new stable ID.

#### Scenario: Consumer renamed
- **WHEN** a consumer unit named by stable ID is renamed and its job reads
- **THEN** the server still matches the rule and returns the outputs.

#### Scenario: Consumer removed and made again
- **WHEN** a consumer unit is removed and made again under the same name
- **THEN** the server denies the read with 403, error code unit.forbidden, until a new rule names the new stable ID.

#### Scenario: Job with no unit attachment
- **WHEN** a job token with no consumer unit attachment requests outputs of another unit
- **THEN** the server denies the read with 403, error code unit.forbidden.

### Requirement: Fail closed on audit failure

If the audit write for an outputs read or denial fails, then the server SHALL fail the request with 500 and return no outputs.

#### Scenario: Audit write fails on read
- **WHEN** the audit write fails during a successful outputs read
- **THEN** the server fails the read with 500 and returns no outputs.

#### Scenario: Audit write fails on denial
- **WHEN** the audit write fails during a denied outputs read
- **THEN** the server returns 500 with no outputs.
