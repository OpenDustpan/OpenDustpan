## Context

Operators install from built artifacts, not from source. The image must come from one version input and carry a known console build.

## Decisions

1. **One version input with pinned console tarball.** One workflow input VERSION sets the server version stamp, the image tag, and the console tarball tag. The Dockerfile takes CONSOLE_TARBALL_SHA256 as a build argument and refuses a build when the hash does not match. The shell script repeats the same hash check before it calls buildx so a wrong input fails fast with a clear message. The Dockerfile check stays as the layer that stops a direct docker build that skips the script.
2. **Local multi-arch build with buildx and QEMU, no push.** Buildx builds linux/amd64 and linux/arm64 in one local run with no push step and no registry. QEMU runs the foreign arch for the smoke step. No publish step runs on a pull request and no push runs on a tag in this change. Publishing waits for the registry decision.

## Structure

No new service type. The guard is the NEW shell script `scripts/release-build-image.sh` that checks CONSOLE_TARBALL_SHA256 before it calls buildx. No unexported type removes the need for the check because the check guards external inputs to a build tool, not a Go value.

## Risks

- Console tarball tag and hash drift causes a failed image build.
- QEMU smoke step is slow on hosted runners.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| build reproducibility | 100 | % | repeated local image builds from the same VERSION input |
| smoke time | 600 | s | make image-smoke on amd64 runner |
| version match | 100 | % of images where server --version equals the image tag | one built image |
