---
version: 2
---

## Why

**Persona: operator.** Operator installs, upgrades, and runs a server from built archives. Today the only install path is go build from source, so there is no tested archive to download and check.

The user path this change must make work:

1. **CLI.** Run the NEW Make target `release-build`, which calls the script `scripts/release-build.sh` of main (changed in place), for a version to produce four archives and SHA256SUMS
2. **CLI.** Run the NEW Make target `release-verify` for the same version to check checksums, files, licenses, and stamp

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Bad version string, nothing built. | Build refuses the value and changes nothing. Use a version of the form v followed by three dot separated numbers, for example v0.1.0. Run the NEW Make target `make release-build VERSION=v0.1.0`. | `make release-build VERSION=v0.1.0` |
| Missing license file, nothing built. | Build refuses and names the missing file. Restore the LICENSE file at the tree root, then run `make licenses` to check license files. | `make licenses` |
| Checksum mismatch for one archive file. | Verification refuses the set and names the first bad file. Rebuild the set, then run the NEW Make target `make release-verify VERSION=v0.1.0`. | `make release-verify VERSION=v0.1.0` |
| Version stamp mismatch in one binary. | Verification refuses the set and names the archive. Rebuild with the same version stamp, then run the NEW Make target `make release-verify VERSION=v0.1.0`. | `make release-verify VERSION=v0.1.0` |

Evidence for the claims above:

- Today the only install path is go build from source. Proof: Roadmap step: Today the only install path is go build from source.
- Release ships SHA256SUMS with no signature and no SBOM. Proof: ADR 283 decision 4 text in pack: Signing and an SBOM come later (owner). v0.1.0 ships the SHA256SUMS file of ADR 27 decision 6.

## What Changes

- Version stamped Linux and macOS tar.gz archives with SHA256SUMS and license files
- The NEW Make targets `release-build` and `release-verify`. The script `scripts/release-build.sh` of main changes in place (no second build script): sorted tar order, fixed owner, mode and mtime, `gzip -n`, `-buildvcs=false`. The archive names stay `opendustpan_<version>_<os>_<arch>.tar.gz` and the record check stays first. The NEW script `scripts/release-verify.sh` checks a set
- Refusal of bad version, missing license, checksum mismatch, missing file, and bad stamp

Out of scope:

- Container image build and registries
- Console tarball pin
- Install test of each artifact against persona path
- User docs install page
- Tagging or publishing a release
- Signatures and SBOM

## Usability

Where the path still costs the person time or surprises them:

- Operator must type an exact version of the form v0.1.0, and the build refuses any other value with the exact form and the one command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The script `scripts/release-build.sh` already builds the four platforms, packs the binary, LICENSE, README.md and third_party_licenses/, writes SHA256SUMS, and runs `scripts/check-release-record.sh` first (`scripts/release-build.sh:33`; `internal/releaserecord/releaserecord_test.go:801 (TestReleaseBuildChecksTheRecordFirst)`)
- Release build variant is already tested against journal version zero (`internal/api/pulumi/journal_release_internal_test.go:10 (TestReleaseBuildAnswersJournalVersionZero)`)
- Backup check already refuses a manifest with no checksum (`pkg/backup/check_checksum_test.go:78 (TestCheckRefusesAManifestWithNoChecksum)`)
- OpenAPI route is already checked to be the same for every build (`pkg/dustpan/apidesc_test.go:644 (TestOpenAPIRouteIsTheSameForEveryBuild)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 27: TLS and release artifacts** (not owner-tagged).

- Decisions that apply: 4 (current), 6 (changed).
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Chain to the end: ADR 212, ADR 246, ADR 278, ADR 283, ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 315, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote: "Version. The flag `--version` prints the version. The build stamps the value with `-ldflags`."
- Quote: "Binaries. A workflow that a tag starts builds the binaries with a `go build` matrix. It makes `tar.gz` and `zip` archives and a `SHA256SUMS` file. The project does not use GoReleaser."
- Effect: Follows decision 4 with ldflags stamp and decision 6 with tag started go build matrix and SHA256SUMS without GoReleaser. Reads decision 6 through ADR 278 current rule: tar.gz only, zip dropped. Reads decision 2 through ADR 212 decision 4.
- Conflict: none.

**ADR 278: Supported platforms are Linux and macOS** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Supported platforms are Linux and macOS"
- Effect: Limits ADR 27 decision 6 to Linux and macOS tar.gz archives. No zip archive ships. The four archives cover linux/amd64, linux/arm64, darwin/amd64, and darwin/arm64.
- Conflict: none.

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current), 6 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No OS packages (owner). `v0.1.0` ships binary archives, the container image and the Helm chart. It ships no `.deb`, `.rpm` or other OS package."
- Quote: "Signing and an SBOM come later (owner). `v0.1.0` ships the `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the archives or of the image, and no SBOM. A later ADR decides them."
- Quote: "Release workflows are a gate item. The gate of `v0.1.0` gets the item "Release workflows for the binaries, the image and the console tarball". A `v*` tag starts the workflow of the binaries and the image."
- Effect: Ships only unsigned tar.gz archives plus SHA256SUMS with no OS packages, no signatures, and no SBOM. The roadmap word signed is read as checksummed only per this owner rule. Builds and tests artifacts only; no tag and no publish.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How are version and archives produced? ADR 27 decision 4 answers it: stamp with -ldflags and report it from --version. ADR 27 decision 6 answers the archive set, as narrowed by ADR 278 to tar.gz for Linux and macOS.
- What ships and what is deferred? ADR 283 decisions 3 and 4 answer it: only unsigned tar.gz archives plus SHA256SUMS, no deb or rpm, no signature, no SBOM.
- Does this change tag or publish a release? The milestone scope answers it: tagging and publishing stay out of scope, this part builds and tests only.
- How is the roadmap word signed read? ADR 283 decision 4 answers it: the roadmap word signed is read as SHA256SUMS checksums only, with no signature shipped.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
