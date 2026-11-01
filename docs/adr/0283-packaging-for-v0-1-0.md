# 283. Packaging and installation for v0.1.0

Status: accepted · 2026-10-30. Decided by the project owner on 30 October 2026. Accepts [RFC 35](../rfc/0035-packaging.md). Decides ADR 27 decision 7. Narrows ADR 27 decision 8 for `v0.1.0`

Changed by:

- ADR 315 extends: the container registries of v0.1.0.

## Context

[RFC 35](../rfc/0035-packaging.md) asks how the project ships the server and
where TLS ends. Two ADRs decide parts of it:

- [ADR 27](0027-tls-and-release-artifacts.md) decides built-in TLS from
  files, the reverse proxy setup, the `--version` flag, the container image
  and the binary archives. Its decision 7 holds back the image release
  workflow until the owner says yes to two GitHub Actions:
  `docker/setup-buildx-action` and `docker/setup-qemu-action`. Its decision 8
  leaves ACME, image signing, an SBOM, the registry and OS packages for later.
- [ADR 246](0246-a-helm-chart-for-opendustpan-serve.md) decides the Helm
  chart.

The open question of RFC 35 is whether built-in TLS is worth its code, and
whether ACME is in scope. On 12 October 2026 the owner said: "Built-in TLS
is fine but including ACME seems like a stretch."

ADR 279 puts the first tagged release, `v0.1.0`, after the exit tests of
milestones 1 to 7. Its gate has the item "Install docs, with TLS (RFC 35
accepted first)". The repository has no release workflow yet.

On 30 October 2026 the project owner decided what RFC 35 means for
`v0.1.0`. Each decision below with the label "(owner)" states that
decision.

## Decision

1. **TLS from files and a reverse proxy (owner).** For `v0.1.0`, the server
   serves TLS from a certificate file and a key file (ADR 27 decision 1). The
   install docs also describe a TLS-terminating reverse proxy in front of
   the server, with the `--public-url` flag (ADR 27 decision 2). The project
   supports both setups.
2. **No ACME (owner).** The server does not get certificates through ACME in
   `v0.1.0`. A later RFC can ask for it.
3. **No OS packages (owner).** `v0.1.0` ships binary archives, the container
   image and the Helm chart. It ships no `.deb`, `.rpm` or other OS package.
4. **Signing and an SBOM come later (owner).** `v0.1.0` ships the
   `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the
   archives or of the image, and no SBOM. A later ADR decides them.
5. **Multi-arch images with buildx and QEMU (owner).** The owner says yes
   to the GitHub Actions `docker/setup-buildx-action` and
   `docker/setup-qemu-action`. This answers the gate of ADR 27 decision 7.
   The image release workflow uses them to build linux/amd64 and
   linux/arm64.
   - Each `uses:` line pins the action by its full commit SHA, with the
     version tag in a comment.
   - The pinned commit is at least two weeks old when the workflow lands.
     This is the rule of ADR 7 for each tool of the project, not a new
     decision of the owner.
6. **Release workflows are a gate item.** The gate of `v0.1.0` gets the item
   "Release workflows for the binaries, the image and the console tarball".
   A `v*` tag starts the workflow of the binaries and the image. The console
   repository has its own workflow for the tarball (ADR 27 decision 5).
7. **The install docs no longer wait for RFC 35.** The gate item "Install
   docs, with TLS" stays. This ADR meets its condition "RFC 35 accepted
   first".

## Consequences

- RFC 35 gets `status: accepted` and `adr: 283`.
- The roadmap release gate drops "RFC 35 accepted first" and adds the
  release-workflow item of decision 6.
- The install docs must show both TLS setups of decision 1, with the
  restart that a renewed certificate needs (ADR 27).
- An operator who wants ACME uses a reverse proxy that has it.
- An operator who needs a signature or an SBOM for `v0.1.0` can check the
  `SHA256SUMS` file only. The docs say this. They make no compliance claim.
- A later ADR that adds signing, an SBOM, ACME or OS packages supersedes
  the matching decision of this ADR.
