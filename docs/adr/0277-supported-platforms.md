# 277. Supported platforms are Linux and macOS

Status: accepted · 2026-10-27. Decided by the project owner

## Context

The release build makes the binary for these pairs of operating system and
architecture: `linux/amd64`, `linux/arm64`, `darwin/amd64` and
`darwin/arm64`. The script `scripts/release-build.sh` also builds
`windows/amd64` today.

CI runs on Linux and macOS. No CI job runs on Windows. Pull request #2017 added
a Windows-only `processStartTime`. No test of it ever ran in CI, so nobody
checks that it works and nobody owns it.

On 27 October 2026 the project owner decided how the project treats other
platforms.

## Decision

1. The supported platforms are Linux and macOS, on `amd64` and `arm64`.
   (owner)
2. Windows and all other platforms are not supported. The project does not
   promise that the binary works there. The Windows archive of the release
   build is not a promise of support. (owner)
3. The project accepts a contribution for another platform only if it meets
   both conditions: (owner)
   - It causes no regression on Linux or macOS.
   - It needs no build tag. Code for the platform does not sit behind a build
     tag of its own.
4. The project fixes a generic bug that stops a build or a `go vet` run on
   another platform. The project does not accept a platform-specific patch,
   for example code in a file that only one other platform builds. (owner)
5. The project does not own code for another platform. It does not test that
   code in CI and does not promise to keep it. (owner)

## Consequences

- `CLAUDE.md` states the rule for agents and contributors.
- The code of pull request #2017 that is specific to Windows goes back to the
  stub that answers `errStartUnknown`. The fixes in that pull request that are
  generic stay.
- Item 3 of issue #1093 (support for Windows) is out of scope.
- The release script still builds `windows/amd64`. This ADR does not change
  it. A later change can remove that pair, or keep it as a build without
  support.
