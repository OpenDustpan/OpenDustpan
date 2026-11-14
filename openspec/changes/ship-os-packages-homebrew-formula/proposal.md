---
version: 1
---

Issue: #946.

## Problem

An Operator can install the CLI only by unpacking archives by hand. There is no system package, Homebrew formula or AUR package. Owner decision, 13 Nov 2026 (queue P15): provisional, not for v0.1.0; revisit after the first tag.

## Evidence

- No PKGBUILD exists in pkg, internal or cmd on main. Proof: rg -n -F --glob '!*_test.go' 'PKGBUILD' pkg internal cmd -> 0 matches. This does not cover release workflow files, so the gate task re-checks them.
- The license and scan targets exist on main. Proof: Makefile:103 (make target trivy) and Makefile:139 (make target licenses)

## Fix boundary

- Record the license of the packaging tool against the make trivy license policy before use.
- Build deb, rpm and apk packages of the CLI with the license files of make licenses, for Linux amd64 and arm64.
- Publish a Homebrew formula that uses the tar.gz archives of macOS and Linux, and an AUR PKGBUILD that uses the Linux archive, both with hashes from SHA256SUMS.
- Add one install docs page.
- Start only after the first tag, and only after an RFC and the owner decision.

Out of scope:

- A package with telemetry of any kind.
- Image signing and an SBOM.
- Windows packages (Linux and macOS only).

## Check

- Write the failing tests of the spec scenarios in the NEW file packaging/packaging_test.go: a package that holds the binary and the license files, and a hash mismatch refusal. No route: the tests read files and build packages only. Check: go test ./packaging/... fails on main for the new test names
- Write the NEW file docs/rfc/os-packages.md, get the owner decision, then wait for the first tag. Re-check the release workflow files for any package step. No route: this task writes a document only. Check: Owner confirms the revisit after the first tag.
- Add the package build, the formula, the PKGBUILD, the CI install step in a container and the install docs page. NEW files: packaging/nfpm.yaml, packaging/render.go, .github/workflows/packages.yml, docs/guides/install-packages.md. No route: the steps build and install packages only. Check: CI runs `opendustpan --version` in a container for deb, rpm and apk
- Run `brew install` and an AUR build from the release. No route: the commands install the CLI only. Check: The commands exit 0 and print the version
- Usability check from the NEW page docs/guides/install-packages.md alone, then run the full check. Check: make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The make trivy target exists for the license policy. (`Makefile:103 (make target trivy)`)
- The make licenses target exists. (`Makefile:139 (make target licenses)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P15)**, Batch 13: #946 Homebrew and AUR: "not for v0.1.0; revisit after the first tag. Reason: ADR 283 d3 (owner) "No OS packages"; Homebrew and AUR are OS package channels."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 27: TLS and release artifacts** (not owner-tagged).

- Decisions that apply: 6 (changed), 8 (changed).
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Chain to the end: ADR 212, ADR 246, ADR 278, ADR 283, ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 315, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote: "Binaries. A workflow that a tag starts builds the binaries with a `go build` matrix. It makes `tar.gz` and `zip` archives and a `SHA256SUMS` file. The project does not use GoReleaser."
- Quote: "Later, not decided. A Helm chart (RFC 35), ACME, image signing, an SBOM, the choice of registry and OS packages."
- Effect: OS packages are not decided. A GoReleaser-style tool conflicts with decision 6, so an RFC and a short ADR follow the license check.
- Conflict: rfc_needed.

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 6 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Signing and an SBOM come later (owner). `v0.1.0` ships the `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the archives or of the image, and no SBOM. A later ADR decides them."
- Quote: "Release workflows are a gate item. The gate of `v0.1.0` gets the item "Release workflows for the binaries, the image and the console tarball". A `v*` tag starts the workflow of the binaries and the image."
- Effect: ADR 283 says no OS packages in v0.1.0 through its change to ADR 27 decision 8. Integrity relies on SHA256SUMS only. The change waits for the owner.
- Conflict: owner_blocks.

**ADR 278: Supported platforms are Linux and macOS** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Supported platforms are Linux and macOS"
- Effect: Only Linux and macOS archives exist, so the formula and the PKGBUILD use tar.gz archives.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The install page ships in the same pull request.
- Conflict: none.

## Open questions

1. Which packaging tool passes the license policy and builds deb, rpm and apk? Decides: PM.
   - Options: GoReleaser OSS; nFPM.
   - Recommended: nFPM, if its license passes the make trivy policy. Reason: ADR 27 decision 6 rejects GoReleaser. The tool needs an RFC and a short ADR after the license check.
2. Where do the Homebrew tap and the AUR package live, who owns them, and which account publishes? Decides: owner.
   - Options: A tap repository in the project organization and an AUR package that a maintainer owns; Submit to homebrew-core only.
   - Recommended: A tap repository in the project organization and an AUR package that a named maintainer owns. Reason: A publish step needs a token and may need a new GitHub Action, which needs the owner (ADR 27 decision 7).

Not spec-ready: a code check fails: task "Usability check from the NEW page docs/guides/install-packag" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Write the failing tests of the spec scenarios in the NEW fil" names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: `.github/workflows/packages.yml` is named by the design or a task but is not in the touches of the stack plan; the change conflicts with ADR 27: an RFC comes first.

Questions considered and answered:

- Can this ship in v0.1.0? No. Owner decision, 13 Nov 2026 (queue P15): provisional, not for v0.1.0; revisit after the first tag.
- Is a docs page needed? Yes. ADR 214 decision 1.
- Is there signing? No. ADR 283 decision 4: hashes only until a later ADR.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
