## ADDED Requirements

### Requirement: key-authorize

When a caller uses the NEW key routes, the server SHALL allow only callers with the NEW action namespace:manage-key for writes and probe and the NEW action namespace:read-key for reads.

#### Scenario: admin holds key actions
- **WHEN** A namespace admin with namespace:manage-key and namespace:read-key calls the NEW key routes.
- **THEN** The server allows the call and classifies both actions with the NEW key routes.

#### Scenario: caller without action is denied
- **WHEN** A caller without the needed key action calls a NEW key route.
- **THEN** The server answers 403 with no KMS call and no audit content read.

### Requirement: key-set

When a caller with the NEW action namespace:manage-key sets the customer key by SetCustomerKey, the server SHALL store the reference only after the probe passes.

#### Scenario: set with passing probe
- **WHEN** A namespace administrator sends the NEW set route and the probe passes.
- **THEN** The server stores the key reference with method SetCustomerKey and writes a set audit entry in the namespace scope.

#### Scenario: confused deputy probe
- **WHEN** The probe with a random ID passes for the wrong namespace identity.
- **THEN** The server stores nothing and answers 422 with the failed check.

#### Scenario: set with failing probe
- **WHEN** The probe with the namespace identity fails.
- **THEN** The server stores nothing and answers 422 with the failed check.

### Requirement: key-change

When a caller with the NEW action namespace:manage-key changes the customer key by SetCustomerKey, the server SHALL check the new key before it replaces the old reference.

#### Scenario: replace existing key
- **WHEN** A namespace administrator sends the NEW set route while a customer key exists and the new probe passes.
- **THEN** The server runs method SetCustomerKey for the new reference, drops cached open versions, and writes a change audit entry.

### Requirement: key-clear

When a caller with the NEW action namespace:manage-key clears the customer key by ClearCustomerKey, the server SHALL remove the reference and drop cached open versions.

#### Scenario: clear key
- **WHEN** A namespace administrator sends the NEW clear route.
- **THEN** The server runs method ClearCustomerKey, removes the customer reference, drops cached open versions, and writes a clear audit entry.

### Requirement: key-read

When a caller with the NEW action namespace:read-key reads key state, the server SHALL answer the reference and wrapper IDs with no plain text keys.

#### Scenario: read key state
- **WHEN** A caller with the NEW action namespace:read-key reads the NEW read route.
- **THEN** The server answers the key reference, wrapper IDs, and KMS key version without plain text key material.

#### Scenario: unknown namespace
- **WHEN** A caller reads the NEW read route for a namespace that does not exist.
- **THEN** The server answers 404 with no key data.

### Requirement: key-probe

When a caller with the NEW action namespace:manage-key runs the NEW probe route by ProbeCustomerKey, the server SHALL run wrap plus open with the namespace identity and answer the result.

#### Scenario: probe reports failure
- **WHEN** A namespace administrator sends the NEW probe route and the KMS denies the namespace identity.
- **THEN** The server runs method ProbeCustomerKey, answers the failed check, and writes a failed probe audit entry.

#### Scenario: unknown shape
- **WHEN** A caller sends a key reference outside the allowlist shape.
- **THEN** The server answers 422 with no KMS call.

### Requirement: key-cache

While a namespace key version is open, the server SHALL keep it in memory for at most the setting keys.namespaceKeyCacheSeconds from 60 to 3600 seconds per instance.

#### Scenario: cache expiry by setting
- **WHEN** An open namespace key version stays cached past keys.namespaceKeyCacheSeconds.
- **THEN** The server drops the entry after the setting life and opens the version again on next use, and the settings API shows the effective value.

### Requirement: key-serialize

While two key writes race on one namespace, the server SHALL serialize them on the namespace fence.

#### Scenario: concurrent set and clear
- **WHEN** Two administrators send set or clear for one namespace at once.
- **THEN** One write wins on the namespace fence and the other retries, and other instances miss the cache after the fence bump.

### Requirement: key-start

When the start check finds a wrapper ID that the key-encryption key does not open, the server SHALL refuse to start.

#### Scenario: wrapper mismatch refuses start
- **WHEN** The key-encryption key does not open a stored wrapper ID at start.
- **THEN** The server refuses to start and names the wrapper ID.

### Requirement: key-outage

If the KMS is unreachable or the key is revoked, then the server SHALL fail closed with 503 or 403 and write an audit entry.

#### Scenario: KMS outage read
- **WHEN** A read or write needs namespace content and the KMS times out or answers 5xx or rate limit.
- **THEN** The server answers 503 with Retry-After and the NEW error code namespace.key-unavailable.

#### Scenario: revoked key read
- **WHEN** The KMS reports the key disabled or access denied.
- **THEN** The server answers 403 with the NEW error code namespace.key-revoked that names the key reference and drops the cache.

#### Scenario: request without content still works
- **WHEN** A request needs no sealed content of the namespace during a KMS outage.
- **THEN** The server answers the request without a KMS call.

### Requirement: key-tool-api

When a tool request needs sealed content during a KMS outage or revoke, the server SHALL answer the same status in the error shape of each tool API.

#### Scenario: tool API outage shape
- **WHEN** A tool read needs namespace content during a KMS outage or revoke.
- **THEN** The Pulumi API and the TFE-compatible API answer 503 or 403 in the error shape of each API.
