# container-distribution Specification

## Purpose
This capability covers publishing of container images to the forge registry and Docker Hub: one multi-arch digest per final version tag, pushed to both registries with comparison and fail-closed re-runs. It stops at the release workflow; image builds stay outside.

## Requirements

### Requirement: Same digest on both registries

Where a final version tag starts the release workflow, the release workflow SHALL push one multi-arch image digest for linux/amd64 and linux/arm64 to the forge container registry first and then to Docker Hub.

#### Scenario: Tag push publishes one digest twice
- **WHEN** a final version tag starts the release workflow
- **THEN** both references resolve to the same digest, each manifest lists linux/amd64 and linux/arm64, and the comparison step passes

#### Scenario: Digest mismatch fails the run
- **WHEN** the two references resolve to different digests
- **THEN** the workflow fails, prints both digests, and does not announce the tag

#### Scenario: Partial push completes on re-run
- **WHEN** the forge push succeeded and the Docker Hub push failed
- **THEN** the re-run pushes the same digest to Docker Hub and the comparison passes before the tag is announced

#### Scenario: Missing Docker Hub token fails closed
- **WHEN** the Docker Hub token secret is missing at push time
- **THEN** the workflow fails with an error that names the secret setup step and pushes nothing to Docker Hub

#### Scenario: Expired Docker Hub token fails closed
- **WHEN** the Docker Hub token is expired or lacks push scope
- **THEN** the workflow fails with an error that names the rotation step

#### Scenario: Re-run with a different digest refuses
- **WHEN** a registry already holds a different digest for the tag
- **THEN** the workflow refuses to overwrite the tag

#### Scenario: Re-run of a completed tag refuses
- **WHEN** both registries already hold the same digest for the tag
- **THEN** the workflow refuses and changes no tag

### Requirement: Tag allowlist and secret reach

Where a tag starts the release workflow, the release workflow SHALL publish public images only for a final version tag of the form vX.Y.Z with no suffix.

#### Scenario: Pre-release tag publishes nothing
- **WHEN** a pre-release tag with a hyphen suffix starts the workflow
- **THEN** the workflow publishes no image to the forge container registry and publishes no image to Docker Hub

#### Scenario: Untrusted run reaches no secret
- **WHEN** a fork or pull-request run reaches the publish workflow
- **THEN** the run stops before any push step and reads no Docker Hub token

### Requirement: Chart OCI artifact and default registry

Where a final version tag starts the release workflow, the release workflow SHALL push the Helm chart as an OCI artifact to the forge container registry with its default image repository set to the forge container registry.

#### Scenario: Chart installs from the default registry
- **WHEN** an operator installs with default values
- **THEN** helm install pulls the image from the forge registry and the server starts

#### Scenario: Operator switches registry with one value
- **WHEN** an operator sets the image repository override value to Docker Hub
- **THEN** helm install pulls the same digest from Docker Hub

#### Scenario: Chart push failure fails the run
- **WHEN** the chart OCI push to the forge container registry fails
- **THEN** the workflow fails and does not announce the tag

### Requirement: Docs list both pulls

Where the install docs cover v0.1.0, the install page SHALL list the pull command for the forge container registry and the pull command for Docker Hub.

#### Scenario: Operator copies a pull command
- **WHEN** an operator opens the install page for v0.1.0
- **THEN** the page shows both pull commands for the same tag

### Requirement: The install guide is in the user documentation

The install page of the container image SHALL be `docs/guides/install.md`, linked from README.md, and SHALL say where the digest of each release is.

#### Scenario: Operator finds the page
- **WHEN** an operator opens README.md
- **THEN** a link leads to `docs/guides/install.md`

#### Scenario: Pull by digest
- **WHEN** an operator reads the section "Pull by digest"
- **THEN** the page shows the digest form of the reference, and names the summary of the release workflow run and `docker buildx imagetools inspect <image>:<tag>` as the places of the digest
