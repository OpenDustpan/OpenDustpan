## ADDED Requirements

### Requirement: run-no-skip

When the CI go job provides the four DUSTPAN_TEST_S3_* variables, the server test suite SHALL run the pkg/blob/s3 conformance suite to pass without a skip marker.

#### Scenario: Suite runs in CI
- **WHEN** the four DUSTPAN_TEST_S3_* variables have values and the container answers the S3 API
- **THEN** the log shows pkg/blob/s3 ran blobtest.Run to pass and the guard exits 0

#### Scenario: Skip fails the job
- **WHEN** the pkg/blob/s3 -v log holds --- SKIP or the package result is not ok
- **THEN** the guard exits 1 and the go job fails instead of staying green

#### Scenario: Unready container fails fast
- **WHEN** the RustFS container does not answer list-buckets within the step timeout
- **THEN** the go job fails at the S3 readiness wait step before go test starts

#### Scenario: Existing bucket passes
- **WHEN** the bucket step runs on a service that already holds the bucket and create-bucket answers BucketAlreadyOwnedByYou or BucketAlreadyExists
- **THEN** the bucket step exits 0 and go test starts

### Requirement: pinned-image

Where CI starts RustFS, the CI workflow SHALL pin the rustfs/rustfs image by its full digest.

#### Scenario: Digest pinned
- **WHEN** a reviewer reads the go job service block
- **THEN** ci.yml names the full rustfs/rustfs image digest and no floating tag
