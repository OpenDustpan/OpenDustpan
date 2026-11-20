# release-image Specification

## Purpose
The image scripts build and check the container image of a checkout. This spec sets how they take the version that the server in the image prints for `--version`. A bad version value is refused before any `docker` call, so that it cannot reach a shell line of the Dockerfile.

## Requirements

### Requirement: version input of the image scripts

When `IMAGE_VERSION` is set, the scripts `scripts/build-image-local.sh` and `scripts/smoke-image.sh` SHALL pass it as the build argument VERSION, and SHALL refuse a value outside the allowlist before any `docker` call.

#### Scenario: Version is passed to the build
- **WHEN** `IMAGE_VERSION` is `v0.1.0` and the build script runs.
- **THEN** the script calls `docker build` with the build argument `VERSION=v0.1.0`.

#### Scenario: Default version
- **WHEN** `IMAGE_VERSION` is not set.
- **THEN** `build-image-local.sh` passes `VERSION=dev` and `smoke-image.sh` passes `VERSION=smoke`, as on main.

#### Scenario: Bad version is refused before docker
- **WHEN** `IMAGE_VERSION` is empty but set, holds a space, a quote, a dollar sign, a backtick, a semicolon, a newline, a leading dash or dot, or is longer than 64 characters.
- **THEN** the script exits with a non-zero status, prints the allowed characters and calls `docker` zero times.

### Requirement: the Dockerfile checks its inputs

When the Dockerfile builds, it SHALL refuse a VERSION outside the allowlist and the placeholder console pin, each with a message that names the input.

#### Scenario: Bad VERSION build argument
- **WHEN** `docker build` runs with a VERSION that is outside the allowlist, with no script in front of it.
- **THEN** the Go stage stops before `go build` and names the build argument VERSION.

#### Scenario: Placeholder console pin
- **WHEN** `CONSOLE_SHA256` is 64 zeros.
- **THEN** the console stage stops before the download and says to pin the console tag and SHA-256 in the Dockerfile, or to pass `CONSOLE_URL` and `CONSOLE_SHA256`.

#### Scenario: Wrong console checksum
- **WHEN** `CONSOLE_SHA256` is a valid hash that does not match the tarball.
- **THEN** the build stops with no image. This is the behavior on main; the smoke test now proves it.

### Requirement: the image version equals its tag

When an image is built from a version, `opendustpan --version` in that image SHALL print that version.

#### Scenario: Smoke test checks the version
- **WHEN** `make image-smoke VERSION=v0.1.0` runs.
- **THEN** the smoke test builds the image with `VERSION=v0.1.0` and the server binary in the container prints `v0.1.0` for `--version`.

#### Scenario: Tag workflow stamps the tag
- **WHEN** the release workflow builds the image for the tag `v0.1.0`.
- **THEN** the build command holds `--build-arg VERSION=` with the tag and holds no console build argument.

### Requirement: a server starts from a built archive

When the release archives of one version are built, the archive of the host platform SHALL match its line in `SHA256SUMS`, and its binary SHALL print the version and serve `GET /health/ready`.

#### Scenario: Install from the binary archive
- **WHEN** the test builds the archives of a test version, checks the host archive against `SHA256SUMS`, unpacks it and starts `serve` on loopback.
- **THEN** `--version` prints the test version and `GET /health/ready` answers 200.

#### Scenario: Wrong checksum
- **WHEN** the archive bytes differ from the `SHA256SUMS` line.
- **THEN** the checksum step of the test refuses the archive and the test does not start the binary.

#### Scenario: Hostile archive entry
- **WHEN** an archive entry has an absolute path, a dot dot part, a link or a name outside the allowlist.
- **THEN** the unpack step of the test refuses it and writes no file outside the target directory.

### Requirement: install page archive section promises no download

The section "Install from a binary archive" of `docs/guides/install.md` SHALL say that binary archives and `SHA256SUMS` are not available yet, SHALL give no download address and no shell block, and SHALL point to the quickstart and to the local image build script. The page SHALL show the version check of the image.

#### Scenario: Archive section promises no download
- **WHEN** the page test reads the archive section.
- **THEN** the section says "not available yet", holds no shell block, no `releases/download` address and none of the commands `curl`, `tar -xzf` and `sha256sum`, and names `quickstart.md` and `build-image-local.sh`.
