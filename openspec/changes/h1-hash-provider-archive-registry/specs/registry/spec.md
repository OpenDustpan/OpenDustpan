## ADDED Requirements

### Requirement: compute h1 hash

When a publisher publishes a provider archive, the server SHALL compute the H1 digest with the dirhash.h1 algorithm over sorted unpacked file entries and store it on the archive record.

#### Scenario: Publish stores h1
- **WHEN** a publisher publishes a provider archive with valid unpacked bytes.
- **THEN** the stored archive record holds the dirhash.h1 digest over sorted unpacked file entries and tofu providers lock reports the same digest.

#### Scenario: Republish with changed bytes refused
- **WHEN** a publisher republishes the same version with an archive of different H1.
- **THEN** the server refuses the request with 409 registry.version-exists.

#### Scenario: Large archive refused with path forward
- **WHEN** a valid provider archive exceeds 64 MB unpacked or 16 MB in one file.
- **THEN** the server refuses with 400 registry.bad-archive and the limit value and the docs step to split or shrink the archive.

### Requirement: Reject unsafe archive

When a provider archive holds an unsafe entry or exceeds the unpack limits, the server SHALL refuse the publish with 400 registry.bad-archive.

#### Scenario: Traversal entry refused
- **WHEN** an archive holds an absolute path, a dot-dot segment, or a symlink entry.
- **THEN** the server refuses the request with 400 registry.bad-archive and stores no release and leaves no file in the staging directory.

#### Scenario: Limit exceeded refused
- **WHEN** real decompressed bytes exceed 64 MB, file count exceeds 1024, one file exceeds 16 MB, or expansion exceeds 100 times compressed size.
- **THEN** the server refuses the request with 400 registry.bad-archive.

#### Scenario: Declared size lie refused
- **WHEN** zip headers declare small sizes but real decompressed bytes exceed a limit.
- **THEN** the server refuses with 400 registry.bad-archive and stores no release.

#### Scenario: Duplicate name refused
- **WHEN** an archive holds duplicate entry names or names that collide after case folding.
- **THEN** the server refuses with 400 registry.bad-archive.

### Requirement: read protocols from manifest

When a provider archive holds terraform-registry-manifest.json, the server SHALL derive the stored protocols only from that manifest.

#### Scenario: Mismatch refused
- **WHEN** stated protocols differ from the protocols in terraform-registry-manifest.json.
- **THEN** the server refuses the request with 400 registry.invalid and stores no release.

#### Scenario: Match stored
- **WHEN** stated protocols match the protocols in terraform-registry-manifest.json as an order-insensitive set with duplicates ignored.
- **THEN** the stored release holds the manifest protocols.

#### Scenario: Empty manifest list refused
- **WHEN** the manifest lists an empty protocol list.
- **THEN** the server refuses with 400 registry.invalid.

### Requirement: Protocols without manifest

When a provider archive lacks terraform-registry-manifest.json, the server SHALL refuse the publish with 400 registry.invalid, with or without stated protocols.

#### Scenario: Stated protocols without manifest refused
- **WHEN** a publisher states protocols and the archive holds no terraform-registry-manifest.json.
- **THEN** the server refuses the request with 400 registry.invalid and stores no release.

#### Scenario: No manifest and no protocols refused
- **WHEN** the archive holds no manifest and the publisher states no protocols.
- **THEN** the server refuses the request with 400 registry.invalid and stores no release, as the store refuses a release with no protocol.

### Requirement: Malformed manifest

When terraform-registry-manifest.json holds malformed JSON, the server SHALL refuse the publish with 400 registry.bad-archive.

#### Scenario: Bad JSON refused
- **WHEN** terraform-registry-manifest.json holds malformed JSON.
- **THEN** the server refuses the request with 400 registry.bad-archive.

### Requirement: Unknown protocol value

When terraform-registry-manifest.json lists a protocol value outside the known set of 5.0 and 6.0, the server SHALL refuse the publish with 400 registry.invalid.

#### Scenario: Unknown value refused
- **WHEN** the manifest lists a protocol value outside the known set of 5.0 and 6.0.
- **THEN** the server refuses the request with 400 registry.invalid.

### Requirement: Count H1 in equality

When the server compares a republished release with the stored release, the server SHALL include the H1 digest in the SameContent result.

#### Scenario: Same bytes republish accepted
- **WHEN** a republish carries the same H1 and the same manifest protocols.
- **THEN** the server accepts the republish as identical.

### Requirement: Concurrent republish same bytes

While a release version exists, the server SHALL compare and store a same-bytes republish inside one transaction.

#### Scenario: Race same bytes
- **WHEN** two publishers publish the same version with identical bytes at the same time.
- **THEN** both requests report the identical result and one stored release remains.

### Requirement: Concurrent republish changed bytes

While a release version exists, the server SHALL refuse a changed-bytes republish inside one transaction with 409 registry.version-exists.

#### Scenario: Race changed bytes
- **WHEN** two publishers publish the same version with different bytes at the same time.
- **THEN** one request succeeds and the other receives 409 registry.version-exists.

### Requirement: Request guards

When a publish request lacks authorization, carries a wrong content type, or exceeds the request size, the server SHALL refuse in the order 403 then 415 then 413 before unpacking. When a staged provider archive is not a zip, the server SHALL refuse it at publish with registry.bad-archive before it computes the hash.

#### Scenario: Oversize request refused
- **WHEN** a publish body exceeds 32 MB by content length or by counted chunked bytes.
- **THEN** the server refuses with 413 without unpacking and deletes staged bytes.

#### Scenario: Wrong content type refused
- **WHEN** an upload request carries a content type other than application/octet-stream.
- **THEN** the server refuses with 415 without unpacking.

#### Scenario: Archive that is not a zip refused
- **WHEN** a publish request names a staged file as the provider archive and the bytes are not a zip.
- **THEN** the server refuses with 400 registry.bad-archive before the hash and stores nothing.

#### Scenario: Denied principal refused before unpack
- **WHEN** a principal without the publish grant sends a publish request.
- **THEN** the server refuses with 403 without staging bytes.

#### Scenario: Staged bytes cleaned
- **WHEN** a publish crashes or a staged delete fails before the release stores.
- **THEN** no staged bytes remain after the restart scan.

### Requirement: Module uploads skip archive checks

When a publisher uploads a module archive, the server SHALL skip the provider unpack, H1, and manifest checks.

#### Scenario: Module skips manifest
- **WHEN** a publisher uploads a module archive through the uploads route.
- **THEN** the server stores the module release without manifest or H1 checks.
