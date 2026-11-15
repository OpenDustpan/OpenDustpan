## ADDED Requirements

### Requirement: versioned multi-arch image

When a version is given, the server release workflow SHALL build a container image for linux/amd64 and linux/arm64 with that version stamp.

#### Scenario: Build image from version
- **WHEN** The release build runs with one version input.
- **THEN** The build produces one local image tagged with the given version for linux/amd64 and linux/arm64 from distroless static nonroot pinned by digest with no push.

#### Scenario: Refuse wrong console hash
- **WHEN** The console tarball SHA-256 does not match the pinned value.
- **THEN** The build stops with no image.

#### Scenario: Refuse bad version input
- **WHEN** VERSION is empty or holds characters outside dots, digits, and dashes.
- **THEN** The NEW script refuses the input with no image.

#### Scenario: Refuse wrong console tag
- **WHEN** The console tarball tag does not match the pinned tag for the version.
- **THEN** The build stops with no image.

#### Scenario: Version stamp matches tag
- **WHEN** The built image starts.
- **THEN** The server --version output equals the image tag.

### Requirement: pinned console content

Where the image includes the console, the server image SHALL carry the console files in /console from the pinned console tarball tag and SHA-256.

#### Scenario: Console files present
- **WHEN** The image starts.
- **THEN** The image serves the console from /console built only from the pinned tarball with no Node step, and the file list equals the pinned tarball list.

### Requirement: nonroot data volume

When the image runs, the server SHALL run as nonroot with a /data volume.

#### Scenario: Nonroot write to data
- **WHEN** The image runs with a /data mount writable by nonroot.
- **THEN** The server starts as nonroot and writes to /data.

#### Scenario: Unwritable data mount fails fast
- **WHEN** The /data mount is not writable by nonroot.
- **THEN** The container stops and names the /data mount permission.

### Requirement: artifact install run

When the artifact install test runs, the server image SHALL start and answer its health probe and console path.

#### Scenario: Install from image
- **WHEN** The artifact install test runs against the built image.
- **THEN** The test starts the server from the built image and completes the operator path with health check and console reachability with no sign-in.

#### Scenario: Install from binary archive
- **WHEN** The artifact install test runs against the built binary archive.
- **THEN** The test unpacks the binary archive from part 1, checks its SHA256SUMS entry, and starts the server to the same health check.
