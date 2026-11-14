## ADDED Requirements

### Requirement: compute h1 hash

When a publisher publishes a provider archive, the server SHALL compute the H1 digest with the dirhash.h1 algorithm over sorted unpacked file entries and store it on the archive record.

#### Scenario: Publish stores h1
- **WHEN** a publisher publishes a provider archive with valid unpacked bytes.
- **THEN** the stored archive record holds the dirhash.h1 digest over sorted unpacked file entries and tofu providers lock reports the same digest.

#### Scenario: Republish with changed bytes refused
- **WHEN** a publisher republishes the same version with an archive of different H1.
- **THEN** the server refuses the request with 409 registry.content-changed.

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
- **THEN** the server refuses the request with 400 registry.protocol-mismatch and stores no release.

#### Scenario: Match stored
- **WHEN** stated protocols match the protocols in terraform-registry-manifest.json as an order-insensitive set with duplicates ignored.
- **THEN** the stored release holds the manifest protocols.

#### Scenario: Empty manifest list refused
- **WHEN** the manifest lists an empty protocol list.
- **THEN** the server refuses with 400 registry.protocol-mismatch.

### Requirement: Protocols without manifest

When a provider archive lacks terraform-registry-manifest.json, the server SHALL refuse stated protocols with 400 registry.protocol-mismatch.

#### Scenario: Stated protocols without manifest refused
- **WHEN** a publisher states protocols and the archive holds no terraform-registry-manifest.json.
- **THEN** the server refuses the request with 400 registry.protocol-mismatch and stores no release.

#### Scenario: No manifest and no protocols stored empty
- **WHEN** the archive holds no manifest and the publisher states no protocols.
- **THEN** the stored release holds an empty protocol list.

### Requirement: Malformed manifest

When terraform-registry-manifest.json holds malformed JSON, the server SHALL refuse the publish with 400 registry.bad-archive.

#### Scenario: Bad JSON refused
- **WHEN** terraform-registry-manifest.json holds malformed JSON.
- **THEN** the server refuses the request with 400 registry.bad-archive.

### Requirement: Unknown protocol value

When terraform-registry-manifest.json lists a protocol value outside the known set of 5.0 and 6.0, the server SHALL refuse the publish with 400 registry.protocol-mismatch.

#### Scenario: Unknown value refused
- **WHEN** the manifest lists a protocol value outside the known set of 5.0 and 6.0.
- **THEN** the server refuses the request with 400 registry.protocol-mismatch.

### Requirement: serve releases under slug

Where a namespace owns releases, the server SHALL serve its releases under the slug formed as the lowercase stable ID with unsafe characters removed.

#### Scenario: Slug install
- **WHEN** a namespace holds a name outside the registry-safe subset of lowercase letters, digits, and hyphen.
- **THEN** clients fetch the release under the slug path with 200.

#### Scenario: Slug stable after rename
- **WHEN** a namespace renames its display name.
- **THEN** the registry slug path stays identical after the rename and the prior name path still serves existing releases.

#### Scenario: Unknown slug refused
- **WHEN** a client requests a slug that maps to no namespace.
- **THEN** the server refuses with 404 registry.unknown-slug.

#### Scenario: Slug collision resolved
- **WHEN** two namespace IDs strip to the same slug or to an empty string.
- **THEN** the second namespace receives the slug with a short suffix and each slug maps to one namespace.

#### Scenario: Slug wins on conflict
- **WHEN** a slug equals the prior name path of another namespace.
- **THEN** the slug path serves the slug owner and the prior name path serves existing releases of the other namespace.

### Requirement: Count H1 in equality

When the server compares a republished release with the stored release, the server SHALL include the H1 digest in the SameContent result.

#### Scenario: Same bytes republish accepted
- **WHEN** a republish carries the same H1 and the same manifest protocols.
- **THEN** the server accepts the republish as identical.

### Requirement: Missing H1 of old release

Where the stored release lacks H1, the server SHALL compare by SHA256 and backfill H1 on identical republish.

#### Scenario: Old release same bytes accepted
- **WHEN** the stored release lacks H1 and the republish carries the same SHA256.
- **THEN** the server accepts the republish as identical and backfills the H1 digest.

### Requirement: Concurrent republish same bytes

While a release version exists, the server SHALL compare and store a same-bytes republish inside one transaction.

#### Scenario: Race same bytes
- **WHEN** two publishers publish the same version with identical bytes at the same time.
- **THEN** both requests report the identical result and one stored release remains.

### Requirement: Concurrent republish changed bytes

While a release version exists, the server SHALL refuse a changed-bytes republish inside one transaction with 409 registry.content-changed.

#### Scenario: Race changed bytes
- **WHEN** two publishers publish the same version with different bytes at the same time.
- **THEN** one request succeeds and the other receives 409 registry.content-changed.

### Requirement: Request guards

When a publish request lacks authorization, carries a wrong content type, or exceeds the request size, the server SHALL refuse in the order 403 then 415 then 413 before unpacking.

#### Scenario: Oversize request refused
- **WHEN** a publish body exceeds 32 MB by content length or by counted chunked bytes.
- **THEN** the server refuses with 413 without unpacking and deletes staged bytes.

#### Scenario: Wrong content type refused
- **WHEN** a publish request carries a content type other than application/zip.
- **THEN** the server refuses with 415 without unpacking.

#### Scenario: Denied principal refused before unpack
- **WHEN** a principal without the publish grant sends a publish request.
- **THEN** the server refuses with 403 without staging bytes.

#### Scenario: Staged bytes cleaned
- **WHEN** a publish crashes or a staged delete fails before the release stores.
- **THEN** no staged bytes remain after the restart scan.

### Requirement: Show limits as effective values

Where a namespace owns releases, the server SHALL show the request and unpack limits as effective values in the API.

#### Scenario: Limits visible
- **WHEN** a caller reads the registry settings of a namespace.
- **THEN** the API reports 32 MB request, 64 MB unpacked, 1024 files, and 16 MB per file.

### Requirement: Module uploads skip archive checks

When a publisher uploads a module archive, the server SHALL skip the provider unpack, H1, and manifest checks.

#### Scenario: Module skips manifest
- **WHEN** a publisher uploads a module archive through the uploads route.
- **THEN** the server stores the module release without manifest or H1 checks.
