## ADDED Requirements

### Requirement: purge-namespace-id

Where blob purges apply, the server SHALL store the namespace ID on each blob_purges row.

#### Scenario: delete and re-create name
- **WHEN** a namespace name is deleted and re-created after purge rows were written
- **THEN** old blobs stay tied to the old namespace ID and never match the new namespace

### Requirement: purge-row-without-id

If a blob_purges row holds no namespace ID, then the server SHALL skip the row during purge reads.

#### Scenario: row without ID is skipped with a record
- **WHEN** a blob_purges row holds no namespace ID
- **THEN** the reader skips the row, deletes no blob, and writes one refusal

### Requirement: cross-workspace-list

When the blob store lists a key outside the requesting workspace prefix, the server SHALL refuse the key with blob.ErrOutOfScope.

#### Scenario: foreign key in list
- **WHEN** the blob store List answers a key of another workspace
- **THEN** the server deletes only blobs under the requesting workspace prefix

### Requirement: openstateblob-hides-key

When OpenStateBlob fails to parse or look up a key, the server SHALL return error text without key material.

#### Scenario: parse error hides key
- **WHEN** OpenStateBlob fails to parse a key
- **THEN** the error text omits the key

### Requirement: refuse-background-channel

While a background sweep records an out-of-scope refusal, the server SHALL write the refusal with the background-work channel.

#### Scenario: sweep refuse channel
- **WHEN** a background sweep records an out-of-scope refusal
- **THEN** the refusal carries the background-work channel

### Requirement: list-empty-workspace

When list receives an empty workspace name, the server SHALL refuse the call.

#### Scenario: empty workspace name
- **WHEN** list receives an empty workspace name
- **THEN** the server refuses the call without listing blobs
