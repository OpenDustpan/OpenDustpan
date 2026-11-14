---
version: 4
---

## Why

**Persona: operator.** Operator installs the server from released artifacts. Today the only path is build from source, so the operator cannot trust that a tagged build installs and runs. This change gives a tested image and clear install steps.

The user path this change must make work:

1. **Docs page.** Read install-from-artifacts docs page and pick image or binary archive.
2. **CLI.** Pull and run the versioned container image with /data mount.
3. **API.** Check server health and open console from the running image.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Console tarball hash mismatch, build refused. | The build stops with no image. Fix the pinned CONSOLE_TARBALL_SHA256 value or the console tag in the workflow input, then rerun the NEW script `scripts/release-build-image.sh`. | `scripts/release-build-image.sh 0.1.0` |
| Image smoke test fails to start the server. | The smoke step fails before any persona check. Check the container log for the /data mount permission, then rerun the NEW Make target `make image-smoke VERSION=0.1.0`. | `make image-smoke VERSION=0.1.0` |

Evidence for the claims above:

- Main has an image test that fails closed. Proof: e2e/image_test.go with TestImageFailsClosed at e2e/image_test.go:72
- Main serves console control paths from the server. Proof: TestConsoleControlPaths at pkg/dustpan/console_control_test.go:14
- Today the only install path is go build from source. Proof: Milestone roadmap step: Today the only install path is go build from source.

## What Changes

- Multi-arch image build from one VERSION with buildx and QEMU and pinned console tarball
- Artifact install test that runs the persona path against the built image
- Install-from-artifacts docs page for image and binary archive installs

Out of scope:

- Binary archive build and checksum file from part 1, reused here without change
- Tagging or publishing a release, including any registry push
- Image signing and SBOM
- OS packages
- Helm chart changes
- ACME

Depends on:

- release-artifacts-binary-archives
- `release-artifacts-binary-archives` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator must copy one VERSION string across binary, image, and console tarball; the single input removes the copy step but the docs must show it once.
- QEMU smoke output is long; the docs show the one success line to look for.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has an image test that checks fail closed behavior. (`e2e/image_test.go:72 (func TestImageFailsClosed)`)
- Main tests console control paths. (`pkg/dustpan/console_control_test.go:14 (func TestConsoleControlPaths)`)
- Main keeps the OpenAPI route stable across builds. (`pkg/dustpan/apidesc_test.go:644 (func TestOpenAPIRouteIsTheSameForEveryBuild)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 27: TLS and release artifacts** (not owner-tagged).

- Decisions that apply: 5 (current), 7 (extended).
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Chain to the end: ADR 212, ADR 246, ADR 278, ADR 283, ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 315, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote: "The image supports linux/amd64 and linux/arm64."
- Quote: "The console static files are in `/console`. They come from a console release tarball."
- Quote: "The image release workflow needs the GitHub Actions `docker/setup-buildx-action` and `docker/setup-qemu-action`."
- Effect: This change builds the image as decided and does not publish it. It uses buildx and QEMU only for a local multi-arch build. It pushes to no registry.
- Conflict: none.

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current), 5 (current), 6 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "The image release workflow uses them to build linux/amd64 and linux/arm64."
- Quote: "A `v*` tag starts the workflow of the binaries and the image."
- Effect: This change uses the owner yes for buildx and QEMU with pinned SHAs. It adds only the local image build check of the tagged workflow. It adds no signature, no SBOM, no OS package, and no registry push.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which tools build the multi-arch image? Buildx and QEMU. ADR 27 decision 7 names them and ADR 283 decision 5 gives the owner yes with SHA pinning.
- Does the image get a signature or an SBOM? No. ADR 283 decision 4 keeps SHA256SUMS only. Signing and SBOM come later.
- Does this change add OS packages? No. ADR 283 decision 3 ships binary archives, image and chart only. No deb or rpm.
- Where does the workflow push the image? No push. ADR 27 decision 8 leaves the registry undecided, and ADR 27 decision 7 keeps the publishing workflow gated. The build runs local buildx only with no push step.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
