---
version: 7
---

## Why

**Persona: operator.** The operator installs the server from a release, not from source. The scripts that build and check the binary archives are on main. The upload of the archives to a release is not: `.github/workflows/release.yml` builds and pushes the image only. The image build, the image smoke test and the first install page are on main too. The first image that the tag workflow would build has three faults that no test shows today:

- The tag workflow builds the image with no version argument. The binary of that image prints `dev`, not the tag.
- The tag workflow passes no console tarball URL and no SHA-256. The Dockerfile then uses its placeholder pin, and the build stops at the download with a curl error that does not name the pin.
- The install page does not say how to install from a binary archive. No test starts a server from a built archive.

The user path this change must make work:

1. **Docs page.** Read `docs/guides/install.md` and pick the image or the binary archive.
2. **CLI, image.** Run `docker run --rm ghcr.io/opendustpan/opendustpan:v0.1.0 --version` and read `v0.1.0`, the tag.
3. **Docs page, archive.** Read the section "Install from a binary archive". It says that archives and `SHA256SUMS` are not available yet, gives no download address, and points to the quickstart and to `./scripts/build-image-local.sh`.
4. **API.** `GET /health/ready` answers 200 on the running server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The version for a local image build is empty or holds a character outside letters, digits, dots, plus and dash. | `build-image-local.sh` and `smoke-image.sh` refuse before any `docker` call and print the allowed set. The Dockerfile refuses the same value. | `IMAGE_VERSION=v0.1.0 scripts/build-image-local.sh` |
| The console pin is the placeholder. | The Dockerfile stops in the console stage and says to pin the console tag and SHA-256 in the Dockerfile, or to pass `CONSOLE_URL` and `CONSOLE_SHA256`. | `docker build --build-arg CONSOLE_URL=<https URL> --build-arg CONSOLE_SHA256=<sha256> .` |
| The console tarball SHA-256 does not match. | The build stops with no image. This is on main (`Dockerfile:56`). The smoke test now proves it. | `make image-smoke` |

## What Changes

- `scripts/build-image-local.sh` and `scripts/smoke-image.sh` take the version from the environment variable `IMAGE_VERSION`, validate it with an allowlist before any `docker` call, and pass it as the build argument. The smoke test checks that `--version` prints that version. `make image-smoke VERSION=v0.1.0` passes it.
- The Dockerfile validates `VERSION` again in the Go stage and refuses the placeholder console pin with a message that names the pin.
- `scripts/smoke-image.sh` gets two refusal cases that run a real build: a wrong console SHA-256 and a bad version.
- A new test builds the release archives in a copy of the work tree, checks the archive of the host platform against `SHA256SUMS`, runs its binary with `--version`, starts `serve` and reads `GET /health/ready`.
- `docs/guides/install.md` gets the section "Check the version of the image" and the section "Install from a binary archive". The archive section says that archives and `SHA256SUMS` are not available yet, gives no download address, and points to the quickstart and the local image build script. A test checks this.
- The release workflow passes `--build-arg VERSION` to the image build. The Dockerfile pin stays the single source of the console pin, so the workflow passes no console argument. A person applies this diff: it is under "Workflow patch (apply locally)" in the pull request body, and the task list contains no workflow file.

Out of scope:

- Tagging or publishing a release, and any registry push. `release.yml` already pushes on a tag; this change does not run it.
- The archive build and `SHA256SUMS` scripts. They are on main and unchanged. The upload of the archives and of `SHA256SUMS` to a release is not on main and is not in this change. ADR 283 (owner-tagged) decisions 3 and 4 say that `v0.1.0` ships the archives and `SHA256SUMS`. A follow-up change that adds the archive build, verify and upload to the tag workflow must land before the `v0.1.0` tag. The install page does not promise an archive download until then.
- Image signing, an SBOM, OS packages, ACME and Helm chart changes.
- A check that the console tag equals the server tag. No ADR asks for it. The change checks only that the console pin is a pin, not the placeholder.
- The real console pin. No console release is known to this change (UNVERIFIED: the check of the console repository releases was not possible in the session that wrote this change). The pin lands in the commit that tags, and the new Dockerfile guard stops a tag build before then.
- A new docs-check skip reason for a download from a release that does not exist yet.

Depends on:

- `release-artifacts-binary-archives`. Its code is on main (`scripts/release-build.sh`, `scripts/release-verify.sh`, `Makefile:198-212`, merged in #3263), but its change directory is still in `openspec/changes/`. This change does not archive it.

## Usability

Where the path still costs the person time or surprises them:

- The operator must see the same version string in the tag, in `--version` of the image and in `--version` of the archive binary. The page shows both commands next to each other.
- The archive section says that archives and `SHA256SUMS` are not available yet and gives no download address, so that no link fails. It names the two ways that work today.

## What main already does

Each fact names its file, line and function on main (head `8c76edc53`). The change specifies only the difference.

- The Dockerfile stamps the version through `-ldflags "-X main.version=${VERSION}"` in a shell `RUN` (`Dockerfile:25`), with `ARG VERSION=dev` (`Dockerfile:17`). The ARG has no check.
- The console stage downloads the tarball from `CONSOLE_URL` and checks `CONSOLE_SHA256` with `sha256sum -c` (`Dockerfile:44-45`, `:56`). The defaults are a `v0.1.0` URL and an all-zero SHA-256. The build argument is named `CONSOLE_SHA256`, not `CONSOLE_TARBALL_SHA256`.
- The base image is distroless static `nonroot`, pinned by digest (`Dockerfile:63`). The image runs as `nonroot:nonroot` (`Dockerfile:79`).
- `scripts/build-image-local.sh` builds one architecture with `docker build` and `--build-arg VERSION=dev` fixed (`scripts/build-image-local.sh:51-52`). It reads its settings from the environment, not from arguments.
- `scripts/smoke-image.sh` builds with `VERSION=smoke` fixed (`scripts/smoke-image.sh:80`), checks the fail-closed start (`:102`), the trial command (`:109`), the namespace create (`:150`), the API (`:155`), the console page (`:164`), the healthy status (`:169`), `--version` equal to `smoke` (`:184-185`), the non-root user (`:189`) and the license files (`:201`). `make image-smoke` runs it (`Makefile:449`).
- `.github/workflows/release.yml` builds `linux/amd64,linux/arm64` with `docker buildx build ... --push .` and no `--build-arg` (`.github/workflows/release.yml:161`). It uses the pinned `docker/setup-qemu-action` (`:84`) and `docker/setup-buildx-action` (`:86`), and pushes to the forge registry and then to Docker Hub.
- `scripts/release-build.sh` checks the version against `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$` (`scripts/release-build.sh:30`), runs the record check, builds four archives and writes `SHA256SUMS` (`:87`). `scripts/release-verify.sh` checks a set (`scripts/release-verify.sh:1-20`). `make release-build` and `make release-verify` call them (`Makefile:198-212`).
- `TestReleaseArchivesAreReproducible` builds the archives in a copy of the work tree with an extended release record and checks their layout (`internal/releaserecord/release_archives_test.go:32`). `TestReleaseVerifyRefusesBadInput` checks the verify script (`internal/releaserecord/release_verify_test.go:324`). Neither test runs a binary from an archive.
- The binary prints the stamped version for `--version` (`cmd/opendustpan/commands.go:64`; `cmd/opendustpan/main_test.go:708`). A build with no stamp prints `dev` (`cmd/opendustpan/main.go:144`).
- `docs/guides/install.md` shows the image pull, the pull by digest, the Helm install and the Docker Hub secrets. It has no binary archive section and no `--version` command. The page is in `docs/commands-pending-pages.txt` (line 19); `docs/commands-checked-pages.txt` is empty, so no docs command is run by `make docs-commands-check` today.
- An image test reads the Dockerfile and checks the lines that make it fail closed (`e2e/image_test.go:72 (func TestImageFailsClosed)`). It does not run docker. Two tests run the binary with the command of the image (`e2e/image_test.go:120 (func TestImageCommandWithoutKey)`, `e2e/image_test.go:155 (func TestImageTrialCommand)`).
- The server serves the console control paths (`pkg/dustpan/console_control_test.go:14 (func TestConsoleControlPaths)`).
- The OpenAPI route is the same for every build (`pkg/dustpan/apidesc_test.go:644 (func TestOpenAPIRouteIsTheSameForEveryBuild)`). This change adds no route, so the API description stays as it is.
- `GET /health/ready` answers 200 (`pkg/dustpan/workerwatch_internal_test.go:471`).

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" are from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)".

**ADR 27: TLS and release artifacts** (not owner-tagged; the owner delegated it to an agent session and can supersede it).

- Decisions that apply: 4, 5, 7.
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote, decision 4: "The flag `--version` prints the version. The build stamps the value with `-ldflags`."
- Quote, decision 5: "The image supports linux/amd64 and linux/arm64."
- Quote, decision 5: "The console static files are in `/console`. They come from a console release tarball. The build pins the tarball by tag and SHA-256. The image build uses no Node."
- Quote, decision 7: "The image release workflow needs the GitHub Actions `docker/setup-buildx-action` and `docker/setup-qemu-action`."
- Effect: the change keeps the pin by tag and SHA-256 and the build with no Node. It adds a refusal of the placeholder pin and a check of the version stamp. Decision 6 (archives with a `zip` file) is changed by ADR 278; the change does not touch it.
- Conflict: none.

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged: decisions 1 to 5 carry "(owner)"; decisions 6 and 7 are not tagged).

- Decisions that apply: 3, 4, 5, 6, 7.
- Changed by: ADR 315 extends. Current rule: this ADR, with ADR 315.
- Quote, decision 3: "`v0.1.0` ships binary archives, the container image and the Helm chart. It ships no `.deb`, `.rpm` or other OS package."
- Quote, decision 4: "`v0.1.0` ships the `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the archives or of the image, and no SBOM."
- Quote, decision 5: "The image release workflow uses them to build linux/amd64 and linux/arm64."
- Quote, decision 6: "A `v*` tag starts the workflow of the binaries and the image."
- Quote, decision 7: "The gate item "Install docs, with TLS" stays."
- Effect: the change adds no signature, no SBOM and no OS package. The change adds an install page section for the archive that says archives are not available yet; the TLS and reverse proxy part of the gate item "Install docs, with TLS" stays with the existing TLS pages and is not in this change.
- Conflict: none.

**ADR 315: Container registries, the quickstart, the lock event, the history scan and the rules for the docs site** (owner-tagged).

- Decision that applies: 1. Changed by: none in the index. Current rule: this ADR.
- Quote, decision 1: "Distribution for v0.1.0: forge container registry plus Docker Hub (organization already registered)".
- Effect: the change keeps both registries as `release.yml` has them and adds no registry.
- Conflict: none.

**ADR 278: Supported platforms are Linux and macOS** (owner-tagged).

- Decisions that apply: 1, 2.
- Quote, decision 1: "The supported platforms are Linux and macOS, on `amd64` and `arm64`."
- Quote, decision 2: "The release build makes no archive for them."
- Effect: the archive install test runs on Linux and macOS hosts on `amd64` and `arm64` only and skips on any other host. The page shows no Windows command.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged; decided by the PM under the owner's decision rule).

- Decisions that apply: 1.
- Quote, decision 1: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: the `--version` command, `IMAGE_VERSION` and the archive path are visible. The two page sections are in the same pull request.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged: "The project owner decided it.").

- Decisions that apply: 1, 2, 4.
- Quote, decision 1: "Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote, decision 4: "The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: the version goes into a shell `RUN` in the Dockerfile, so it is security input. The script and the Dockerfile each validate it, and each has its own test. The archive test reads the checksum and the archive entries as untrusted file data.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the console pin live? In the Dockerfile `CONSOLE_URL` and `CONSOLE_SHA256` defaults. The comment at `Dockerfile:39-42` says so, and ADR 27 decision 5 says "The build pins the tarball by tag and SHA-256." The real values wait for the first console release.
- Does the image get a signature or an SBOM? No. ADR 283 decision 4.
- Does the workflow push in this change? No. The workflow patch is applied by a person, and the change runs no tag.
- Must the console tag equal the server tag? No ADR says so. The change does not decide it.
