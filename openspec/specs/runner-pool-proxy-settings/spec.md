# runner-pool-proxy-settings Specification

## Purpose
Runner pool proxy settings let an administrator with the runner grants set the allowed private ranges and the upstream proxy of a pool and read the effective values. The credential of the upstream proxy is stored sealed and no read returns it. A save is versioned and writes one audit entry. It does not cover how the job proxy enforces the ranges.

## Requirements

### Requirement: Read the effective proxy settings of a pool

The server SHALL show the effective allowed private ranges and upstream proxy of a pool, with the place that set them, and SHALL NOT return the upstream credential.

#### Scenario: Pool with no settings
- **WHEN** a reader with `namespace:read-runners` reads the proxy settings of a pool that has none
- **THEN** the server returns empty ranges, no upstream URL, `credentialSet` false, `source` default and version 0

#### Scenario: Pool with settings
- **WHEN** a reader reads the proxy settings of a pool that has them
- **THEN** the server returns the ranges, the upstream URL, `credentialSet`, `source` set, the version, who set them and when, and no credential

#### Scenario: Read without grant
- **WHEN** a principal without `namespace:read-runners` reads the settings
- **THEN** the server refuses with access denied

### Requirement: Save the proxy settings of a pool

When a principal with `namespace:manage-runners` saves valid settings, the server SHALL store them, seal the credential, raise the version and write an audit entry in the same transaction.

#### Scenario: Save valid settings
- **WHEN** a principal with `namespace:manage-runners` saves a valid range list and an upstream URL with a credential
- **THEN** the server stores the row, seals the credential under the scope of the namespace and the pool, and writes an audit entry with the old and new ranges, digests of the old and new URL and `credentialChanged`

#### Scenario: Keep and remove the credential
- **WHEN** a save has no `upstreamCredential` field
- **THEN** the stored credential stays
- **WHEN** a save has `upstreamCredential` set to null
- **THEN** the server removes the credential and the audit entry says `credentialChanged`

#### Scenario: Save without grant
- **WHEN** a principal without `namespace:manage-runners` saves the settings
- **THEN** the server refuses the save and changes nothing

#### Scenario: Audit write fails
- **WHEN** the audit write fails during a save
- **THEN** the save fails and the old settings stay

### Requirement: Validate the ranges and the upstream URL

The server SHALL accept a range only when `egress.ParseAllowedRanges` accepts it, and SHALL accept an upstream URL only when it is http or https, has a host, no user information and no fragment, and does not name a never-opened address.

#### Scenario: Range that never opens
- **WHEN** a save holds a range that covers loopback, link-local, the metadata address, an unspecified or multicast address, or is shorter than the minimum prefix
- **THEN** the server answers 400 `invalid-range`, names the range and changes nothing

#### Scenario: Upstream URL with user information
- **WHEN** a save holds an upstream URL with a user name or a password in it
- **THEN** the server answers 400 `invalid-upstream` and the text does not repeat the URL

### Requirement: Version check on save

When a save sends If-Match with a version that is not the stored version, the server SHALL refuse it with 412 and change nothing.

#### Scenario: Stale version
- **WHEN** a save sends If-Match with an old version
- **THEN** the server answers 412 and the stored settings do not change

#### Scenario: No If-Match
- **WHEN** a save sends no If-Match
- **THEN** the server saves and raises the version

### Requirement: Settings in the backup without the plain credential

A backup SHALL hold the pool proxy settings with the credential sealed, and SHALL NOT hold proxy handoff tokens or proxy sessions.

#### Scenario: Backup and restore
- **WHEN** an operator makes a backup and restores it
- **THEN** the settings come back with the sealed credential and no proxy session exists
