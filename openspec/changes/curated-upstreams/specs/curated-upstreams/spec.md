## ADDED Requirements

### Requirement: curation-enabled setting

The server SHALL store a curation-enabled setting per namespace that defaults to off and exposes its effective value in the API.

#### Scenario: Default off with effective value
- **WHEN** a new namespace is created
- **THEN** the API reports curation-enabled false as the effective value.

#### Scenario: Toggle on and off
- **WHEN** a namespace administrator turns curation on and then off
- **THEN** the API reports the new effective value and stored rules stay unchanged.

#### Scenario: Docs page toggle
- **WHEN** a namespace administrator follows the docs page
- **THEN** the docs page shows the toggle steps and the effective value.

### Requirement: create-rule

The server SHALL store each curation rule keyed by its namespace ID with an upstream host, a registry address prefix, and a semver constraint.

#### Scenario: List rules
- **WHEN** a namespace administrator creates a curation rule
- **THEN** the server lists each rule with host, address prefix, and semver constraint for that namespace ID.

#### Scenario: Edit rule
- **WHEN** a namespace administrator edits one curation rule
- **THEN** the server stores the edited host, prefix, and constraint for that namespace ID.

### Requirement: reject invalid rule

When a curation rule holds an invalid host, an empty prefix, an unparsable semver constraint, or an IP literal host, the server SHALL return 400 without storing the rule.

#### Scenario: Invalid rule fields
- **WHEN** a rule holds an invalid host, an empty prefix, an unparsable semver constraint, or an IP literal host
- **THEN** the server returns 400 without storing the rule.

### Requirement: authorize rule management

When a principal without the namespace curation manage action edits a curation rule or the curation-enabled setting, the server SHALL return 403.

#### Scenario: Forbidden edit
- **WHEN** a principal without the namespace curation manage action edits a rule or the setting
- **THEN** the server returns 403 without changing rules or settings.

### Requirement: match all rule fields

When curation is on, the server SHALL allow a public release only when host, registry address prefix at a path boundary, and semver constraint of one rule all match the request.

#### Scenario: Close name refused
- **WHEN** the host differs by one character from the rule host
- **THEN** the server refuses the download.

#### Scenario: Wider prefix refused
- **WHEN** the registry address shares leading characters with the prefix across a path boundary
- **THEN** the server refuses the download.

#### Scenario: Version outside constraint refused
- **WHEN** the version falls outside the rule semver constraint
- **THEN** the server refuses the download.

### Requirement: combine rules and versions

When curation is on and more than one rule exists for the namespace, the server SHALL allow the download when at least one rule matches host, prefix, and constraint.

#### Scenario: Second rule allows
- **WHEN** the first rule misses and the second rule matches host, prefix, and constraint
- **THEN** the server allows the download.

#### Scenario: Prerelease excluded
- **WHEN** the version is a prerelease outside the rule semver constraint
- **THEN** the server refuses the download.

### Requirement: match host exactly over https

When curation is on, the server SHALL match the rule host exactly without subdomain wildcard and fetch the upstream host over HTTPS only.

#### Scenario: Subdomain refused
- **WHEN** the request host is a subdomain of the rule host
- **THEN** the server refuses the download.

#### Scenario: Plain http refused
- **WHEN** the matched upstream address uses plain http
- **THEN** the server refuses the upstream fetch.

### Requirement: refuse unlisted public release

When curation is on and no curation rule matches the requested public release, the server SHALL refuse the download with 403 before it checks the cache.

#### Scenario: Blocked provider artifact
- **WHEN** curation is on and no rule matches the requested public OpenTofu provider release
- **THEN** the server refuses the download with 403 that names the registry address before it checks the cache.

#### Scenario: Blocked module artifact
- **WHEN** curation is on and no rule matches the requested public OpenTofu module release
- **THEN** the server refuses the download with 403 that names the registry address before it checks the cache.

#### Scenario: Blocked plugin artifact
- **WHEN** curation is on and no rule matches the requested public Pulumi plugin
- **THEN** the server refuses the download with 403 that names the Pulumi plugin name before it checks the cache.

### Requirement: fetch and cache allowed release

When curation allows the request and the cache holds no release for the registry address, the server SHALL fetch the bytes once from the matched upstream host and store them in cache before it serves.

#### Scenario: Cache miss fetches provider
- **WHEN** curation allows an OpenTofu provider release that the cache does not hold
- **THEN** the server fetches the bytes once from the matched upstream host, stores them with checksum in cache, then serves them.

#### Scenario: Cache miss fetches module
- **WHEN** curation allows an OpenTofu module release that the cache does not hold
- **THEN** the server fetches the bytes once from the matched upstream host, stores them with checksum in cache, then serves them.

#### Scenario: Cache miss fetches plugin
- **WHEN** curation allows a Pulumi plugin release that the cache does not hold
- **THEN** the server fetches the bytes once from the matched upstream host, stores them with checksum in cache, then serves them.

### Requirement: verify cached release

When the server fetches an allowed public release, the server SHALL verify its checksum and enforce a size limit and a time limit before it stores the bytes in cache.

#### Scenario: Bad checksum discarded
- **WHEN** the fetched bytes fail checksum verify
- **THEN** the server discards the bytes, stores no cache entry, and returns 502.

#### Scenario: Oversize response refused
- **WHEN** the upstream response passes the size or time limit
- **THEN** the server stops the fetch and stores no cache entry.

### Requirement: share single flight

While two fetches for the same namespace address miss the cache at the same time, the server SHALL run one upstream fetch and discard partial bytes that never passed verify.

#### Scenario: Concurrent misses share fetch
- **WHEN** two fetches for the same namespace address miss the cache at the same time
- **THEN** one upstream fetch runs and each waiter gets the same verified result.

#### Scenario: Partial store never serves
- **WHEN** a fetch stops or the instance stops before verify and store complete
- **THEN** a later fetch refetches from upstream.

### Requirement: evaluate-fetch

When curation is on and one curation rule matches the requested public release and the audit write succeeds, the server SHALL serve the cached release.

#### Scenario: Allowed tofu provider
- **WHEN** curation is on and one rule matches the tofu init provider request
- **THEN** the server returns the cached provider bytes with its checksum.

#### Scenario: Allowed tofu module
- **WHEN** curation is on and one rule matches the tofu init module request
- **THEN** the server returns the cached module bytes with its checksum.

#### Scenario: Allowed Pulumi plugin
- **WHEN** curation is on and one rule matches the Pulumi CLI plugin request
- **THEN** the server returns the cached plugin bytes with its checksum.

### Requirement: key cache by namespace

When curation is on, the server SHALL key each cache entry by namespace ID and re-check the snapshot before each serve.

#### Scenario: Old cache entry not served
- **WHEN** curation is on, no rule matches, and the cache holds bytes stored before curation was on
- **THEN** the server refuses the download with 403.

#### Scenario: Removed rule stops serve
- **WHEN** a namespace administrator removes the only matching rule
- **THEN** later fetches for that address are refused with 403.

### Requirement: skip check when off

While curation is off for a namespace, the server SHALL serve the cached release without a curation check.

#### Scenario: Curation off
- **WHEN** curation is off for the namespace
- **THEN** the server serves the cached release without a curation check and without a curation audit entry.

### Requirement: refuse missing upstream release

When curation allows the request but the cache and the matched upstream host hold no release for the registry address, the server SHALL return 404.

#### Scenario: No upstream release
- **WHEN** curation allows the request but the cache and the matched upstream host hold no release for the address
- **THEN** the server returns 404 that names the registry address.

### Requirement: refuse upstream failure

When the matched upstream host fails with 5xx, timeout, or DNS failure, the server SHALL return 502 that names the registry address.

#### Scenario: Upstream 5xx or timeout
- **WHEN** the matched upstream host fails with 5xx, timeout, or DNS failure
- **THEN** the server returns 502 that names the registry address and stores no cache entry.

### Requirement: record curation decision

When the server allows or refuses a public release through curation, the server SHALL write one audit entry with the verdict before it serves bytes.

#### Scenario: Audit record
- **WHEN** the server allows or refuses a public release through curation
- **THEN** the audit log holds one entry with the principal, the registry address, and the allow or refuse verdict before the serve.

#### Scenario: Audit write fails
- **WHEN** the audit write for a curation decision fails
- **THEN** the server refuses the download, writes the failure to the server log, and serves no bytes.

### Requirement: reject hostile address

When the registry address of a fetch holds an encoded traversal, an absolute URL, or a path escape, the server SHALL return 400 without a fetch.

#### Scenario: Traversal address
- **WHEN** the registry address holds an encoded traversal, an absolute URL, or a path escape
- **THEN** the server returns 400 without a fetch.

### Requirement: stop unlisted redirect

When an upstream redirect points to a host outside the matched rule, the server SHALL stop the fetch.

#### Scenario: Redirect to unlisted host
- **WHEN** an upstream redirect points to a host outside the matched rule
- **THEN** the server stops the fetch and records the refusal.

### Requirement: use request snapshot

While a fetch runs, the server SHALL decide under the rule snapshot from request start.

#### Scenario: Rule change mid fetch
- **WHEN** a rule changes while a fetch runs
- **THEN** the fetch completes under the old snapshot and the next fetch uses the new rules.

### Requirement: concurrent rule edits

While two administrators edit the same curation rule at the same time, the server SHALL apply last-write-wins to the stored rule.

#### Scenario: Two administrators edit
- **WHEN** two administrators edit the same rule at the same time
- **THEN** the last write wins and the API lists the last stored rule.
